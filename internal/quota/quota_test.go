package quota

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeBinary is the test executable, which doubles as a fake CLI.
func fakeBinary(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	return path
}

func TestCodexReadReportsExhaustedWindow(t *testing.T) {
	codex := Codex{Binary: fakeBinary(t), Env: fakeEnv("codex"), Timeout: 30 * time.Second}
	snapshot, err := codex.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if snapshot.Provider != "codex" || snapshot.AccountID == "" {
		t.Fatalf("unexpected snapshot identity: %+v", snapshot)
	}
	if len(snapshot.Windows) != 2 {
		t.Fatalf("want 2 windows, got %d", len(snapshot.Windows))
	}
	if !snapshot.Exhausted() {
		t.Fatal("snapshot should report an exhausted limit")
	}
	primary := findWindow(t, snapshot, "codex:primary")
	if primary.UsedPercent != 100 || primary.Label != "5-hour" {
		t.Fatalf("unexpected primary window: %+v", primary)
	}
	if !primary.ResetsAt.Equal(time.Unix(1793020473, 0).UTC()) {
		t.Fatalf("unexpected reset time: %s", primary.ResetsAt)
	}
}

func TestCodexReadRejectsSignedOutAccount(t *testing.T) {
	codex := Codex{Binary: fakeBinary(t), Env: fakeEnv("codex_no_account"), Timeout: 30 * time.Second}
	if _, err := codex.Read(context.Background()); err == nil {
		t.Fatal("want an error for a signed-out account")
	}
}

func TestClaudeReadReportsExhaustedWindow(t *testing.T) {
	claude := Claude{Binary: fakeBinary(t), Env: fakeEnv("claude"), Timeout: 30 * time.Second}
	snapshot, err := claude.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if snapshot.Provider != "claude" {
		t.Fatalf("unexpected provider: %s", snapshot.Provider)
	}
	session := findWindow(t, snapshot, "claude:five_hour")
	if session.UsedPercent != 100 || session.Label != "5-hour" {
		t.Fatalf("unexpected session window: %+v", session)
	}
	if !snapshot.Exhausted() {
		t.Fatal("snapshot should report an exhausted limit")
	}
}

func TestClaudeReadRejectsSignedOut(t *testing.T) {
	claude := Claude{Binary: fakeBinary(t), Env: fakeEnv("claude_signed_out"), Timeout: 30 * time.Second}
	if _, err := claude.Read(context.Background()); err == nil {
		t.Fatal("want an error for a signed-out account")
	}
}

func TestClaudeReadRejectsMissingLimits(t *testing.T) {
	claude := Claude{Binary: fakeBinary(t), Env: fakeEnv("claude_no_limits"), Timeout: 30 * time.Second}
	if _, err := claude.Read(context.Background()); err == nil {
		t.Fatal("want an error when no plan limits are reported")
	}
}

func TestDecodeClaudeTreatsMissingNumbersAsTemporary(t *testing.T) {
	init := []byte(`{"account":{"email":"a@b.c"}}`)
	empty := []byte(`{"rate_limits_available":true,"subscription_type":"pro"}`)
	if _, err := decodeClaude(init, empty, time.Now()); err == nil || !strings.Contains(err.Error(), "will retry") {
		t.Fatalf("an empty reply must be a temporary error, got %v", err)
	}
	noPlan := []byte(`{"rate_limits_available":false}`)
	if _, err := decodeClaude(init, noPlan, time.Now()); err == nil || !strings.Contains(err.Error(), "no plan limits") {
		t.Fatalf("an account without plan limits must say so, got %v", err)
	}
}

func TestDecodeCodexHandlesLegacyShape(t *testing.T) {
	account := []byte(`{"account":{"type":"chatgpt","email":"a@b.c"}}`)
	limits := []byte(`{"rateLimits":{"primary":{"usedPercent":50,"windowDurationMins":300,"resetsAt":1793020473}}}`)
	snapshot, err := decodeCodex(account, limits, time.Now())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snapshot.Windows) != 1 {
		t.Fatalf("want 1 window, got %d", len(snapshot.Windows))
	}
	if snapshot.Exhausted() {
		t.Fatal("half-used window must not be exhausted")
	}
}

func TestDecodeClaudeHandlesLimitsArray(t *testing.T) {
	init := []byte(`{"account":{"email":"a@b.c"}}`)
	usage := []byte(`{"rate_limits":{"limits":[
      {"kind":"session","percent":100,"resets_at":"2026-10-26T18:00:00Z"},
      {"kind":"weekly_scoped","percent":40,"resets_at":"2026-11-01T09:00:00Z","scope":"opus"}]}}`)
	snapshot, err := decodeClaude(init, usage, time.Now())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snapshot.Windows) != 2 {
		t.Fatalf("want 2 windows, got %d", len(snapshot.Windows))
	}
	if !snapshot.Exhausted() {
		t.Fatal("session window at 100% must be exhausted")
	}
}

func findWindow(t *testing.T, snapshot Snapshot, id string) Window {
	t.Helper()
	for _, window := range snapshot.Windows {
		if window.ID == id {
			return window
		}
	}
	t.Fatalf("window %s not found in %+v", id, snapshot.Windows)
	return Window{}
}

// TestLiveProbes talks to the real CLIs. It only runs with CARRYON_LIVE=1.
func TestLiveProbes(t *testing.T) {
	if os.Getenv("CARRYON_LIVE") != "1" {
		t.Skip("set CARRYON_LIVE=1 to probe the real CLIs")
	}
	ctx := context.Background()
	for _, provider := range []Provider{Codex{}, Claude{}} {
		snapshot, err := provider.Read(ctx)
		if err != nil {
			t.Errorf("%s: %v", provider.Name(), err)
			continue
		}
		t.Logf("%s: %d windows, exhausted=%v", provider.Name(), len(snapshot.Windows), snapshot.Exhausted())
		for _, window := range snapshot.Windows {
			t.Logf("  %s %s used=%.1f%% resets=%s", window.ID, window.Label, window.UsedPercent, window.ResetsAt)
		}
	}
}

func TestClaudeProbeTimesOut(t *testing.T) {
	claude := Claude{Binary: fakeBinary(t), Env: fakeEnv("hang"), Timeout: 300 * time.Millisecond}
	started := time.Now()
	if _, err := claude.Read(context.Background()); err == nil {
		t.Fatal("want an error when the probe never answers")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("probe took %s; timeout was not enforced", elapsed)
	}
}

func TestCodexProbeTimesOut(t *testing.T) {
	codex := Codex{Binary: fakeBinary(t), Env: fakeEnv("hang"), Timeout: 300 * time.Millisecond}
	started := time.Now()
	if _, err := codex.Read(context.Background()); err == nil {
		t.Fatal("want an error when the probe never answers")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("probe took %s; timeout was not enforced", elapsed)
	}
}

func TestCodexProbeStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	codex := Codex{Binary: fakeBinary(t), Env: fakeEnv("hang"), Timeout: 30 * time.Second}
	started := time.Now()
	if _, err := codex.Read(ctx); err == nil {
		t.Fatal("want an error after cancellation")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("probe ignored cancellation for %s", elapsed)
	}
}
