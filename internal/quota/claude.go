package quota

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ph03iA/CarryOn/internal/config"
	"github.com/ph03iA/CarryOn/internal/winjob"
)

// Claude reads plan limits from Claude Code's get_usage control request, the
// same data behind /usage. No prompt is written, so no model request starts.
// shortcut: get_usage is experimental upstream and may change; keep it isolated here.
type Claude struct {
	Binary  string
	Timeout time.Duration
	// Env overrides the child environment; nil uses the scrubbed parent.
	Env []string
}

// Name identifies the provider.
func (Claude) Name() string { return "claude" }

// Read starts Claude Code in stream-json mode and asks for usage only.
func (c Claude) Read(ctx context.Context) (Snapshot, error) {
	binary, err := ResolveBinary("claude", c.Binary, nil)
	if err != nil {
		return Snapshot{}, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	process, err := winjob.Start(winjob.Spec{
		Path: binary,
		Args: []string{
			"--output-format", "stream-json",
			"--verbose",
			"--input-format", "stream-json",
			"--strict-mcp-config",
			"--setting-sources", "",
		},
		Dir:     os.TempDir(),
		Env:     c.environment(),
		Stderr:  io.Discard,
		Timeout: timeout,
	})
	if err != nil {
		return Snapshot{}, err
	}
	defer process.Close()
	stop := supervise(ctx, process, timeout)
	defer stop()

	conn := newClaudeConn(process.Stdin, process.Stdout)
	init, err := conn.request("initialize", map[string]any{"subtype": "initialize"})
	if err != nil {
		return Snapshot{}, err
	}
	usage, err := conn.request("usage", map[string]any{"subtype": "get_usage", "skip_behaviors": true})
	if err != nil {
		return Snapshot{}, err
	}
	return decodeClaude(init, usage, time.Now().UTC())
}

// environment disables MCP servers and IDE attachment for the probe.
func (c Claude) environment() []string {
	env := c.Env
	if env == nil {
		env = config.ChildEnvironment()
	}
	return append(env,
		"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"CLAUDE_CODE_AUTO_CONNECT_IDE=0",
		"CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL=1",
	)
}

type claudeConn struct {
	stdin io.Writer
	lines *bufio.Scanner
}

func newClaudeConn(stdin io.Writer, stdout io.Reader) *claudeConn {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), maxLineBytes)
	return &claudeConn{stdin: stdin, lines: scanner}
}

type claudeEnvelope struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Response  *struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
		Error     string          `json:"error"`
	} `json:"response"`
}

func (c *claudeConn) request(id string, request map[string]any) (json.RawMessage, error) {
	message := map[string]any{"type": "control_request", "request_id": id, "request": request}
	data, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write control request: %w", err)
	}
	for c.lines.Scan() {
		var envelope claudeEnvelope
		if json.Unmarshal(c.lines.Bytes(), &envelope) != nil {
			continue
		}
		if envelope.Type != "control_response" || envelope.Response == nil {
			continue
		}
		if envelope.Response.RequestID != id {
			continue
		}
		if envelope.Response.Subtype != "success" {
			return nil, fmt.Errorf("claude %s failed: %s", request["subtype"], envelope.Response.Error)
		}
		return envelope.Response.Response, nil
	}
	if err := c.lines.Err(); err != nil {
		return nil, fmt.Errorf("read control stream: %w", err)
	}
	return nil, errors.New("claude exited before answering; run claude once to finish sign-in")
}

type claudeInit struct {
	Account *struct {
		Email string `json:"email"`
	} `json:"account"`
}

type claudeUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	Percent     *float64 `json:"percent"`
	ResetsAt    *string  `json:"resets_at"`
}

type claudeUsage struct {
	SubscriptionType    string `json:"subscription_type"`
	RateLimitsAvailable bool   `json:"rate_limits_available"`
	RateLimits          *struct {
		FiveHour *claudeUsageWindow `json:"five_hour"`
		SevenDay *claudeUsageWindow `json:"seven_day"`
		Limits   []claudeLimit      `json:"limits"`
	} `json:"rate_limits"`
}

type claudeLimit struct {
	Kind        string   `json:"kind"`
	Percent     *float64 `json:"percent"`
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
	Scope       *string  `json:"scope"`
}

func decodeClaude(initRaw, usageRaw json.RawMessage, now time.Time) (Snapshot, error) {
	var init claudeInit
	if err := json.Unmarshal(initRaw, &init); err != nil {
		return Snapshot{}, errors.New("claude returned an unreadable initialize response")
	}
	accountID, err := claudeAccountID(init)
	if err != nil {
		return Snapshot{}, err
	}
	var usage claudeUsage
	if err := json.Unmarshal(usageRaw, &usage); err != nil {
		return Snapshot{}, errors.New("claude returned unreadable usage data")
	}
	if usage.RateLimitsAvailable && usage.RateLimits == nil {
		// Seen live: Claude Code sometimes answers without the numbers when asked
		// often. The last good reading stays in place until the next check.
		return Snapshot{}, errors.New("claude returned no usage numbers this time; will retry")
	}
	windows := claudeWindows(usage)
	if len(windows) == 0 {
		return Snapshot{}, errors.New("claude reported no plan limits for this account")
	}
	for index := range windows {
		windows[index].Provider = "claude"
		windows[index].AccountID = accountID
	}
	return Snapshot{Provider: "claude", AccountID: accountID, ObservedAt: now, Windows: windows}, nil
}

func claudeAccountID(init claudeInit) (string, error) {
	if init.Account == nil || strings.TrimSpace(init.Account.Email) == "" {
		return "", errors.New("claude is not signed in; run claude and sign in")
	}
	email := strings.ToLower(strings.TrimSpace(init.Account.Email))
	sum := sha256.Sum256([]byte("claude:" + email))
	return hex.EncodeToString(sum[:]), nil
}

func claudeWindows(usage claudeUsage) []Window {
	if usage.RateLimits == nil {
		return nil
	}
	if len(usage.RateLimits.Limits) > 0 {
		return claudeListedWindows(usage.RateLimits.Limits)
	}
	pairs := []struct {
		id       string
		duration time.Duration
		data     *claudeUsageWindow
	}{
		{"claude:five_hour", 5 * time.Hour, usage.RateLimits.FiveHour},
		{"claude:seven_day", 7 * 24 * time.Hour, usage.RateLimits.SevenDay},
	}
	var windows []Window
	for _, pair := range pairs {
		window, ok := claudeWindowValue(pair.id, pair.duration, pair.data.used(), pair.data.reset())
		if ok {
			windows = append(windows, window)
		}
	}
	return windows
}

func (w *claudeUsageWindow) used() *float64 {
	if w == nil {
		return nil
	}
	if w.Utilization != nil {
		return w.Utilization
	}
	return w.Percent
}

func (w *claudeUsageWindow) reset() *string {
	if w == nil {
		return nil
	}
	return w.ResetsAt
}

func claudeListedWindows(limits []claudeLimit) []Window {
	var windows []Window
	for _, limit := range limits {
		id, duration := claudeKind(limit.Kind, limit.Scope)
		percent := limit.Percent
		if percent == nil {
			percent = limit.Utilization
		}
		window, ok := claudeWindowValue(id, duration, percent, limit.ResetsAt)
		if ok {
			windows = append(windows, window)
		}
	}
	return windows
}

func claudeKind(kind string, scope *string) (string, time.Duration) {
	switch kind {
	case "session":
		return "claude:five_hour", 5 * time.Hour
	case "weekly_scoped":
		suffix := "weekly_scoped"
		if scope != nil && strings.TrimSpace(*scope) != "" {
			suffix = "weekly_scoped:" + strings.ToLower(strings.TrimSpace(*scope))
		}
		return "claude:" + suffix, 7 * 24 * time.Hour
	default:
		return "claude:" + kind, 7 * 24 * time.Hour
	}
}

func claudeWindowValue(id string, duration time.Duration, percent *float64, resetsAt *string) (Window, bool) {
	if percent == nil || resetsAt == nil {
		return Window{}, false
	}
	reset := parseReset(resetsAt)
	if reset.IsZero() {
		return Window{}, false
	}
	return Window{
		ID:          id,
		Label:       Label(duration),
		UsedPercent: *percent,
		ResetsAt:    reset,
		Duration:    duration,
	}, true
}

// parseReset reads an RFC3339 reset time, rounded to the minute because Claude
// reports sub-second drift between calls.
func parseReset(value *string) time.Time {
	if value == nil {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC().Round(time.Minute)
}
