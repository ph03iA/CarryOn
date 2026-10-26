package mail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResendSendsIdempotencyKeyAndBody(t *testing.T) {
	var seenKey, seenAuth string
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenKey = request.Header.Get("Idempotency-Key")
		seenAuth = request.Header.Get("Authorization")
		payload, _ := io.ReadAll(request.Body)
		json.Unmarshal(payload, &seenBody)
		writer.WriteHeader(http.StatusOK)
		writer.Write([]byte(`{"id":"abc"}`))
	}))
	defer server.Close()

	sender := &Resend{APIKey: "re_test", BaseURL: server.URL, Client: server.Client()}
	err := sender.Send(context.Background(), Message{
		EventKey: "limit:abc", To: "me@example.com", From: "onboarding@resend.dev",
		Subject: "subject", Body: "body",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if seenKey != "carryon-limit:abc" {
		t.Fatalf("unexpected idempotency key: %q", seenKey)
	}
	if seenAuth != "Bearer re_test" {
		t.Fatalf("unexpected auth header: %q", seenAuth)
	}
	if seenBody["subject"] != "subject" || seenBody["from"] != "onboarding@resend.dev" {
		t.Fatalf("unexpected body: %+v", seenBody)
	}
	to, _ := seenBody["to"].([]any)
	if len(to) != 1 || to[0] != "me@example.com" {
		t.Fatalf("unexpected recipients: %+v", seenBody["to"])
	}
}

func TestResendClassifiesRetryableFailures(t *testing.T) {
	cases := []struct {
		status    int
		retryable bool
	}{
		{http.StatusOK, false},
		{http.StatusBadRequest, false},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusRequestTimeout, true},
	}
	for _, testCase := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(testCase.status)
		}))
		sender := &Resend{APIKey: "re_test", BaseURL: server.URL, Client: server.Client()}
		err := sender.Send(context.Background(), Message{EventKey: "k", To: "a@b.c", From: "x@y.z"})
		if testCase.status == http.StatusOK {
			if err != nil {
				t.Fatalf("status %d: unexpected error %v", testCase.status, err)
			}
			server.Close()
			continue
		}
		if err == nil {
			t.Fatalf("status %d: want an error", testCase.status)
		}
		if Retryable(err) != testCase.retryable {
			t.Fatalf("status %d: retryable=%v want %v", testCase.status, Retryable(err), testCase.retryable)
		}
		server.Close()
	}
}

func TestResendReadsRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Retry-After", "42")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	sender := &Resend{APIKey: "re_test", BaseURL: server.URL, Client: server.Client()}
	err := sender.Send(context.Background(), Message{EventKey: "k", To: "a@b.c", From: "x@y.z"})
	if RetryAfter(err) != 42*time.Second {
		t.Fatalf("unexpected retry-after: %v", RetryAfter(err))
	}
}

func TestResendRequiresKey(t *testing.T) {
	sender := &Resend{}
	if err := sender.Send(context.Background(), Message{EventKey: "k"}); err == nil {
		t.Fatal("want an error without an API key")
	}
	if Retryable(&deliveryError{message: "x"}) {
		t.Fatal("a missing key must not be retried")
	}
}

func TestRenderLimitHitUsesLocalTime(t *testing.T) {
	reset := time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)
	subject, body := Render(Event{
		Kind: KindLimitHit, Provider: "claude", WindowLabel: "5-hour",
		ResetAt: reset, SessionID: "abc", SessionCwd: `C:\work`,
		Now: reset,
	})
	if !strings.Contains(subject, "Claude Code 5-hour limit hit") {
		t.Fatalf("unexpected subject: %s", subject)
	}
	if !strings.Contains(body, "Resets:") || !strings.Contains(body, "UTC") {
		t.Fatalf("reset time missing: %s", body)
	}
	if !strings.Contains(body, `C:\work`) {
		t.Fatalf("folder missing: %s", body)
	}
}

func TestRenderSimulatedIsLabelled(t *testing.T) {
	subject, body := Render(Event{
		Kind: KindResumeFinish, Provider: "codex", SessionID: "s", Action: "done",
		Reason: "task complete", Simulated: true,
	})
	if !strings.HasPrefix(subject, "[SIMULATED] ") {
		t.Fatalf("subject not labelled: %s", subject)
	}
	if !strings.HasPrefix(body, "SIMULATED — no real usage limit was hit.") {
		t.Fatalf("body not labelled: %s", body)
	}
}

func TestRenderFinishListsFilesAndTests(t *testing.T) {
	_, body := Render(Event{
		Kind: KindResumeFinish, Provider: "claude", SessionID: "s",
		Action: "done", Reason: "done", ExitCode: 0,
		LastMessage: "All green.", Files: []string{"a.go", "b.go"},
		TestOutput: "ok  pkg  0.1s",
	})
	for _, want := range []string{"All green.", "a.go", "b.go", "ok  pkg", "Exit status: 0"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}
