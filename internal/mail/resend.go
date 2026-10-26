// Package mail sends CarryOn notifications through Resend.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Message is one email to deliver.
type Message struct {
	EventKey string
	To       string
	From     string
	Subject  string
	Body     string
}

// Sender delivers a message.
type Sender interface {
	Send(ctx context.Context, message Message) error
}

// Resend posts messages to the Resend API.
type Resend struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

// NewResend builds a sender with a sane timeout.
func NewResend(apiKey string) *Resend {
	return &Resend{APIKey: apiKey, Client: &http.Client{Timeout: 30 * time.Second}}
}

type deliveryError struct {
	message    string
	retryable  bool
	retryAfter time.Duration
}

func (e *deliveryError) Error() string { return e.message }

// Retryable reports whether the same message can be retried safely.
func Retryable(err error) bool {
	var failure *deliveryError
	return errors.As(err, &failure) && failure.retryable
}

// RetryAfter returns the provider's requested delay, capped at an hour.
func RetryAfter(err error) time.Duration {
	var failure *deliveryError
	if errors.As(err, &failure) {
		return failure.retryAfter
	}
	return 0
}

// Send posts one email with an idempotency key so retries never duplicate.
func (r *Resend) Send(ctx context.Context, message Message) error {
	if r == nil || strings.TrimSpace(r.APIKey) == "" {
		return &deliveryError{message: "RESEND_API_KEY is not set"}
	}
	payload, err := json.Marshal(map[string]any{
		"from":    message.From,
		"to":      []string{message.To},
		"subject": message.Subject,
		"text":    message.Body,
	})
	if err != nil {
		return err
	}
	endpoint := r.BaseURL
	if endpoint == "" {
		endpoint = "https://api.resend.com/emails"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+r.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey(message.EventKey))

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return &deliveryError{message: "resend request failed: " + err.Error(), retryable: true}
	}
	defer response.Body.Close()
	return interpret(response)
}

// idempotencyKey keeps the key short and stable per event.
func idempotencyKey(eventKey string) string {
	key := "carryon-" + eventKey
	if len(key) > 256 {
		return key[:256]
	}
	return key
}

func interpret(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	failure := &deliveryError{
		message:   fmt.Sprintf("resend returned %d: %s", response.StatusCode, strings.TrimSpace(string(body))),
		retryable: retryableStatus(response.StatusCode),
	}
	if seconds := response.Header.Get("Retry-After"); seconds != "" {
		if parsed, err := strconv.Atoi(seconds); err == nil && parsed > 0 {
			failure.retryAfter = time.Duration(parsed) * time.Second
		}
	}
	return failure
}

func retryableStatus(status int) bool {
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500
}
