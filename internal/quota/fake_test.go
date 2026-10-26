package quota

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMain lets the test binary double as a fake Claude or Codex CLI.
func TestMain(m *testing.M) {
	if mode := os.Getenv("CARRYON_FAKE_MODE"); mode != "" {
		os.Exit(runFake(mode))
	}
	os.Exit(m.Run())
}

func runFake(mode string) int {
	switch mode {
	case "codex":
		return fakeCodex()
	case "claude":
		return fakeClaude()
	case "claude_no_limits":
		return fakeClaudeNoLimits()
	case "claude_signed_out":
		return fakeClaudeSignedOut()
	case "codex_no_account":
		return fakeCodexNoAccount()
	}
	fmt.Fprintln(os.Stderr, "unknown fake mode", mode)
	return 2
}

func fakeCodex() int {
	return serveRPC(func(method string) (any, bool) {
		switch method {
		case "initialize":
			return map[string]any{"userAgent": "fake-codex", "platformFamily": "windows", "platformOs": "windows"}, true
		case "account/read":
			return map[string]any{
				"account":           map[string]any{"type": "chatgpt", "email": "dev@example.com"},
				"requiresOpenaiAuth": true,
			}, true
		case "account/rateLimits/read":
			return map[string]any{
				"rateLimitsByLimitId": map[string]any{
					"codex": map[string]any{
						"limitId": "codex",
						"primary": map[string]any{
							"usedPercent": 100.0, "windowDurationMins": 300, "resetsAt": 1793020473,
						},
						"secondary": map[string]any{
							"usedPercent": 12.0, "windowDurationMins": 10080, "resetsAt": 1793607273,
						},
					},
				},
				"ordinaryUsageAllowed": false,
			}, true
		}
		return nil, false
	})
}

func fakeCodexNoAccount() int {
	return serveRPC(func(method string) (any, bool) {
		switch method {
		case "initialize":
			return map[string]any{"userAgent": "fake-codex"}, true
		case "account/read":
			return map[string]any{"account": nil, "requiresOpenaiAuth": true}, true
		}
		return nil, false
	})
}

// serveRPC answers newline-delimited JSON-RPC until the client goes away.
func serveRPC(handler func(method string) (any, bool)) int {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxLineBytes)
	for scanner.Scan() {
		var request struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		if request.Method == "initialized" || request.ID == nil {
			continue
		}
		result, ok := handler(request.Method)
		if !ok {
			writeLine(map[string]any{
				"id": *request.ID,
				"error": map[string]any{"code": -32601, "message": "unsupported"},
			})
			continue
		}
		writeLine(map[string]any{"id": *request.ID, "result": result})
	}
	return 0
}

func fakeClaude() int {
	return serveControl(func(subtype string) (any, bool) {
		switch subtype {
		case "initialize":
			return map[string]any{
				"account":             map[string]any{"email": "dev@example.com"},
				"claude_code_version": "0.0.0-fake",
			}, true
		case "get_usage":
			return map[string]any{
				"subscription_type": "max",
				"rate_limits": map[string]any{
					"five_hour": map[string]any{"utilization": 100.0, "resets_at": "2026-10-26T18:00:00Z"},
					"seven_day": map[string]any{"utilization": 21.0, "resets_at": "2026-11-01T09:00:00Z"},
				},
			}, true
		}
		return nil, false
	})
}

func fakeClaudeNoLimits() int {
	return serveControl(func(subtype string) (any, bool) {
		if subtype == "initialize" {
			return map[string]any{"account": map[string]any{"email": "dev@example.com"}}, true
		}
		return map[string]any{"rate_limits_available": false, "rate_limits": nil}, true
	})
}

func fakeClaudeSignedOut() int {
	return serveControl(func(subtype string) (any, bool) {
		if subtype == "initialize" {
			return map[string]any{"account": nil}, true
		}
		return map[string]any{"rate_limits_available": true}, true
	})
}

// serveControl answers Claude control_request messages by subtype.
func serveControl(handler func(subtype string) (any, bool)) int {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxLineBytes)
	for scanner.Scan() {
		var request struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		if request.Type != "control_request" {
			continue
		}
		result, ok := handler(request.Request.Subtype)
		response := map[string]any{"subtype": "success", "request_id": request.RequestID, "response": result}
		if !ok {
			response = map[string]any{"subtype": "error", "request_id": request.RequestID, "error": "unsupported"}
		}
		writeLine(map[string]any{"type": "control_response", "response": response})
		if request.Request.Subtype == "get_usage" {
			return 0
		}
	}
	return 0
}

func writeLine(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	os.Stdout.Write(append(data, '\n'))
}

// fakeEnv builds a child environment that carries the fake mode.
func fakeEnv(mode string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "CARRYON_FAKE_MODE=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CARRYON_FAKE_MODE="+mode)
}
