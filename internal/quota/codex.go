package quota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/ph03iA/CarryOn/internal/config"
	"github.com/ph03iA/CarryOn/internal/winjob"
)

// clientVersion is reported to Codex during the handshake.
const clientVersion = "0.1.0"

// Codex reads ChatGPT plan limits from the local app-server.
type Codex struct {
	Binary  string
	Timeout time.Duration
	// Env overrides the child environment; nil uses the scrubbed parent.
	Env []string
}

// Name identifies the provider.
func (Codex) Name() string { return "codex" }

func (c Codex) environment() []string {
	if c.Env != nil {
		return c.Env
	}
	return config.ChildEnvironment()
}

// Read performs the app-server handshake and reads rate limits. It never
// creates a thread or a turn, so no prompt is sent.
func (c Codex) Read(ctx context.Context) (Snapshot, error) {
	binary, err := resolveBinary("codex", c.Binary, codexFallbacks())
	if err != nil {
		return Snapshot{}, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	process, err := winjob.Start(winjob.Spec{
		Path:    binary,
		Args:    []string{"app-server"},
		Env:     c.environment(),
		Stderr:  io.Discard,
		Timeout: timeout,
	})
	if err != nil {
		return Snapshot{}, err
	}
	defer process.Close()

	conn := newRPCConn(process.Stdin, process.Stdout)
	account, limits, err := codexHandshake(conn)
	if err != nil {
		return Snapshot{}, err
	}
	return decodeCodex(account, limits, time.Now().UTC())
}

func codexHandshake(conn *rpcConn) (json.RawMessage, json.RawMessage, error) {
	initialize := map[string]any{
		"method": "initialize", "id": 0,
		"params": map[string]any{
			"clientInfo": map[string]string{"name": "carryon", "title": "CarryOn", "version": clientVersion},
		},
	}
	if err := conn.send(initialize); err != nil {
		return nil, nil, err
	}
	if _, err := conn.await(0); err != nil {
		return nil, nil, err
	}
	if err := conn.send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return nil, nil, err
	}
	if err := conn.send(map[string]any{
		"method": "account/read", "id": 1,
		"params": map[string]any{"refreshToken": false},
	}); err != nil {
		return nil, nil, err
	}
	account, err := conn.await(1)
	if err != nil {
		return nil, nil, err
	}
	if err := conn.send(map[string]any{"method": "account/rateLimits/read", "id": 2}); err != nil {
		return nil, nil, err
	}
	limits, err := conn.await(2)
	return account, limits, err
}

type codexAccount struct {
	Account *struct {
		Type  string `json:"type"`
		Email string `json:"email"`
	} `json:"account"`
}

type codexLimits struct {
	RateLimits           *codexLimit            `json:"rateLimits"`
	RateLimitsByLimitID  map[string]*codexLimit `json:"rateLimitsByLimitId"`
	OrdinaryUsageAllowed *bool                  `json:"ordinaryUsageAllowed"`
}

type codexLimit struct {
	LimitID   string       `json:"limitId"`
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
}

type codexWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *int64   `json:"windowDurationMins"`
	ResetsAt           *int64   `json:"resetsAt"`
}

func decodeCodex(accountRaw, limitsRaw json.RawMessage, now time.Time) (Snapshot, error) {
	var account codexAccount
	if err := json.Unmarshal(accountRaw, &account); err != nil {
		return Snapshot{}, errors.New("codex returned an unreadable account")
	}
	accountID, err := codexAccountID(account)
	if err != nil {
		return Snapshot{}, err
	}
	var limits codexLimits
	if err := json.Unmarshal(limitsRaw, &limits); err != nil {
		return Snapshot{}, errors.New("codex returned unreadable rate limits")
	}
	windows := codexWindows(limits)
	if len(windows) == 0 {
		return Snapshot{}, errors.New("codex reported no usage windows")
	}
	for index := range windows {
		windows[index].Provider = "codex"
		windows[index].AccountID = accountID
	}
	return Snapshot{
		Provider: "codex", AccountID: accountID, ObservedAt: now,
		Windows: windows, OrdinaryUsageAllowed: limits.OrdinaryUsageAllowed,
	}, nil
}

func codexAccountID(account codexAccount) (string, error) {
	if account.Account == nil || account.Account.Type != "chatgpt" {
		return "", errors.New("codex needs a signed-in ChatGPT account; run codex login")
	}
	email := strings.ToLower(strings.TrimSpace(account.Account.Email))
	if email == "" {
		return "", errors.New("codex did not report an account email")
	}
	sum := sha256.Sum256([]byte("codex:" + email))
	return hex.EncodeToString(sum[:]), nil
}

func codexWindows(limits codexLimits) []Window {
	buckets := limits.RateLimitsByLimitID
	if len(buckets) == 0 && limits.RateLimits != nil {
		id := limits.RateLimits.LimitID
		if id == "" {
			id = "codex"
		}
		buckets = map[string]*codexLimit{id: limits.RateLimits}
	}
	var windows []Window
	for id, limit := range buckets {
		if limit == nil {
			continue
		}
		windows = append(windows, codexLimitWindows(id, limit)...)
	}
	return windows
}

func codexLimitWindows(id string, limit *codexLimit) []Window {
	pairs := []struct {
		name   string
		window *codexWindow
	}{{"primary", limit.Primary}, {"secondary", limit.Secondary}}
	var windows []Window
	for _, pair := range pairs {
		window, ok := codexWindowValue(id+":"+pair.name, pair.window)
		if ok {
			windows = append(windows, window)
		}
	}
	return windows
}

func codexWindowValue(id string, data *codexWindow) (Window, bool) {
	if data == nil || data.UsedPercent == nil || data.WindowDurationMins == nil || data.ResetsAt == nil {
		return Window{}, false
	}
	if *data.WindowDurationMins <= 0 {
		return Window{}, false
	}
	duration := time.Duration(*data.WindowDurationMins) * time.Minute
	return Window{
		ID:          id,
		Label:       Label(duration),
		UsedPercent: *data.UsedPercent,
		ResetsAt:    time.Unix(*data.ResetsAt, 0).UTC(),
		Duration:    duration,
	}, true
}

