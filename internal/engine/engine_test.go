package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ph03iA/CarryOn/internal/config"
	"github.com/ph03iA/CarryOn/internal/decision"
	"github.com/ph03iA/CarryOn/internal/logx"
	"github.com/ph03iA/CarryOn/internal/mail"
	"github.com/ph03iA/CarryOn/internal/quota"
	"github.com/ph03iA/CarryOn/internal/resume"
	"github.com/ph03iA/CarryOn/internal/session"
	"github.com/ph03iA/CarryOn/internal/store"
)

const fixtureSessionID = "11111111-1111-4111-8111-111111111111"

type fakeProvider struct {
	name     string
	snapshot quota.Snapshot
	err      error
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Read(context.Context) (quota.Snapshot, error) {
	return f.snapshot, f.err
}

type fakeDecider struct {
	results []decision.Result
	errs    []error
	calls   int
	last    decision.Request
}

func (f *fakeDecider) Decide(_ context.Context, request decision.Request) (decision.Result, error) {
	f.last = request
	index := f.calls
	f.calls++
	if index < len(f.errs) && f.errs[index] != nil {
		return decision.Result{}, f.errs[index]
	}
	if index < len(f.results) {
		return f.results[index], nil
	}
	return decision.Result{Action: decision.ActionDone, Reason: "default"}, nil
}

type fakeRunner struct {
	calls   int
	outcome resume.Outcome
	err     error
	specs   []resume.Spec
}

func (f *fakeRunner) Run(_ context.Context, spec resume.Spec) (resume.Outcome, error) {
	f.calls++
	f.specs = append(f.specs, spec)
	return f.outcome, f.err
}

type fakeSender struct {
	sent []mail.Message
	err  error
}

func (f *fakeSender) Send(_ context.Context, message mail.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, message)
	return nil
}

// harness is one engine wired to fakes and a temp session root.
type harness struct {
	engine   *Engine
	db       *store.DB
	provider *fakeProvider
	decider  *fakeDecider
	runner   *fakeRunner
	sender   *fakeSender
	root     string
	dbPath   string
	now      time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	copyFixture(t, root)
	dbPath := filepath.Join(t.TempDir(), "state.db")

	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	provider := &fakeProvider{name: "claude", snapshot: quota.Snapshot{
		Provider: "claude", AccountID: "acct", ObservedAt: now.Add(-time.Minute),
		Windows: []quota.Window{{
			Provider: "claude", AccountID: "acct", ID: "claude:five_hour", Label: "5-hour",
			UsedPercent: 100, ResetsAt: now.Add(2 * time.Minute), Duration: 5 * time.Hour,
		}},
	}}
	built, db := newEngine(t, dbPath, root, provider, now)
	return &harness{
		engine: built, db: db, provider: provider,
		decider: built.Decider.(*fakeDecider), runner: built.Runner.(*fakeRunner),
		sender: built.Sender.(*fakeSender), root: root, dbPath: dbPath, now: now,
	}
}

func newEngine(t *testing.T, dbPath, root string, provider *fakeProvider, now time.Time) (*Engine, *store.DB) {
	t.Helper()
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	decider := &fakeDecider{results: []decision.Result{
		{Action: decision.ActionResume, Reason: "work remains", Prompt: "finish the retry helper"},
		{Action: decision.ActionDone, Reason: "the task is complete"},
	}}
	built := &Engine{
		DB: db, Config: config.Defaults(),
		Providers: []quota.Provider{provider},
		Scanner:   session.Scanner{ClaudeRoot: root, MaxBytes: 8 << 20},
		Decider:   decider,
		Runner: &fakeRunner{outcome: resume.Outcome{
			ExitCode: 0, LastMessage: "Done: retry helper added",
			Files: []string{"retry.go"}, TestOutput: "ok  demo  0.2s",
		}},
		Sender:      &fakeSender{},
		Log:         logx.Discard(),
		Now:         func() time.Time { return now },
		Synchronous: true,
	}
	return built, db
}

func copyFixture(t *testing.T, root string) {
	t.Helper()
	source := filepath.Join("..", "session", "testdata", "claude", "cutoff", "proj-a", fixtureSessionID+".jsonl")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	target := filepath.Join(root, "proj-a")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, fixtureSessionID+".jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) step(t *testing.T) {
	t.Helper()
	if err := h.engine.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
}

func (h *harness) subjects() []string {
	subjects := make([]string, len(h.sender.sent))
	for index, message := range h.sender.sent {
		subjects[index] = message.Subject
	}
	return subjects
}

func TestLimitHitEmailsOnceAndRecordsSession(t *testing.T) {
	h := newHarness(t)
	h.step(t)
	h.step(t)

	if len(h.sender.sent) != 1 {
		t.Fatalf("want exactly one limit email, got %v", h.subjects())
	}
	if !strings.Contains(h.sender.sent[0].Subject, "limit hit") {
		t.Fatalf("unexpected subject: %s", h.sender.sent[0].Subject)
	}
	incidents, err := h.db.Incidents(context.Background())
	if err != nil || len(incidents) != 1 {
		t.Fatalf("want one incident: %v %v", incidents, err)
	}
	if incidents[0].SessionID != fixtureSessionID {
		t.Fatalf("cut-off session not recorded: %+v", incidents[0])
	}
	if h.runner.calls != 0 {
		t.Fatal("must not resume before the reset is confirmed")
	}
}

func TestResetConfirmationRequiresFreshReading(t *testing.T) {
	h := newHarness(t)
	h.step(t)

	// Move time past the reset but keep the stale reading from before it.
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(-time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if h.runner.calls != 0 {
		t.Fatal("a stale reading must not confirm the reset")
	}
}

func TestFullJourneyWithRestart(t *testing.T) {
	h := newHarness(t)
	h.step(t)
	if len(h.sender.sent) != 1 {
		t.Fatalf("want the limit email first: %v", h.subjects())
	}

	// The reset happens and a fresh reading confirms it.
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if h.runner.calls != 1 {
		t.Fatalf("want one resume run, got %d", h.runner.calls)
	}
	if h.runner.specs[0].SessionID != fixtureSessionID {
		t.Fatalf("resume must target the session id, got %+v", h.runner.specs[0])
	}
	if !strings.HasPrefix(h.runner.specs[0].Prompt, decision.FixedInstruction) {
		t.Fatal("resume prompt must start with the fixed instruction")
	}

	subjects := strings.Join(h.subjects(), "\n")
	for _, want := range []string{"limit hit", "resumed", "finished"} {
		if !strings.Contains(subjects, want) {
			t.Fatalf("missing %q email in:\n%s", want, subjects)
		}
	}
	finish := h.sender.sent[len(h.sender.sent)-1]
	if !strings.Contains(finish.Body, "Done: retry helper added") || !strings.Contains(finish.Body, "retry.go") {
		t.Fatalf("finish email missing outcome:\n%s", finish.Body)
	}

	incidents, _ := h.db.Incidents(context.Background())
	if len(incidents) != 1 || incidents[0].State != store.IncidentFinished {
		t.Fatalf("incident should be finished: %+v", incidents)
	}

	// Restarting must not repeat the work.
	restarted, _ := newEngine(t, h.dbPath, h.root, h.provider, h.now.Add(4*time.Minute))
	if err := restarted.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls := restarted.Runner.(*fakeRunner).calls; calls != 0 {
		t.Fatalf("restart must not resume again, got %d calls", calls)
	}
}

func TestCrashNeedsANewDecisionBeforeResuming(t *testing.T) {
	h := newHarness(t)
	h.step(t)

	incidents, _ := h.db.Incidents(context.Background())
	incident := incidents[0]
	resumeRow, err := h.db.ResumeForSession(context.Background(), "claude", fixtureSessionID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-run: claimed, running, incident resuming.
	if _, err := h.db.ClaimResume(context.Background(), resumeRow.ID, 2, "first prompt"); err != nil {
		t.Fatal(err)
	}
	if err := h.db.StartResume(context.Background(), resumeRow.ID, "d1"); err != nil {
		t.Fatal(err)
	}
	if err := h.db.UpdateIncident(context.Background(), incident.ID, store.IncidentUpdate{
		State: store.IncidentResuming, SessionID: incident.SessionID,
		SessionPath: incident.SessionPath, SessionCwd: incident.SessionCwd,
	}); err != nil {
		t.Fatal(err)
	}

	restarted, _ := newEngine(t, h.dbPath, h.root, h.provider, h.now)
	restarted.Decider.(*fakeDecider).results = []decision.Result{
		{Action: decision.ActionSkipDone, Reason: "the agent had already finished"},
	}
	if err := restarted.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	if err := restarted.Step(context.Background()); err != nil {
		t.Fatal(err)
	}

	if calls := restarted.Runner.(*fakeRunner).calls; calls != 0 {
		t.Fatalf("a crash must not resume without a new decision, got %d runs", calls)
	}
	decisions, err := restarted.DB.Decisions(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) == 0 || decisions[0].Action != decision.ActionSkipDone {
		t.Fatalf("a fresh decision should be recorded: %+v", decisions)
	}
}

func TestDecisionFailureBlocksAndEmails(t *testing.T) {
	h := newHarness(t)
	h.step(t)
	h.decider.errs = []error{errors.New("gemini is down")}
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if h.runner.calls != 0 {
		t.Fatal("must not resume when Gemini fails")
	}
	last := h.sender.sent[len(h.sender.sent)-1]
	if !strings.Contains(last.Subject, "blocked") {
		t.Fatalf("expected a blocked email, got %s", last.Subject)
	}
	incidents, _ := h.db.Incidents(context.Background())
	if incidents[0].State != store.IncidentBlocked {
		t.Fatalf("incident should be blocked: %+v", incidents[0])
	}
}

func TestAlreadyContinuedIsSkipped(t *testing.T) {
	h := newHarness(t)
	h.step(t)

	// Something else continues the session before the reset.
	path := filepath.Join(h.root, "proj-a", fixtureSessionID+".jsonl")
	extra := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Continuing on my own."}]},"uuid":"e0000000-0000-4000-8000-000000000001","timestamp":"2026-10-26T18:00:00.000Z","cwd":"C:\\work\\demo"}` + "\n"
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(extra)
	file.Close()

	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if h.runner.calls != 0 {
		t.Fatal("must not resume a session that was already continued")
	}
	last := h.sender.sent[len(h.sender.sent)-1]
	if !strings.Contains(last.Subject, "skipped") {
		t.Fatalf("expected a skip email, got %s", last.Subject)
	}
}

func TestSimulateIsLabelledAndUsesTheSession(t *testing.T) {
	h := newHarness(t)
	h.engine.Synchronous = true
	if err := h.engine.Simulate(context.Background(), "", fixtureSessionID); err != nil {
		t.Fatalf("simulate: %v", err)
	}
	if h.runner.calls != 1 {
		t.Fatalf("simulate should run once, got %d", h.runner.calls)
	}
	for _, message := range h.sender.sent {
		if !strings.HasPrefix(message.Subject, "[SIMULATED] ") {
			t.Fatalf("simulated email not labelled: %s", message.Subject)
		}
		if !strings.HasPrefix(message.Body, "SIMULATED — no real usage limit was hit.") {
			t.Fatalf("simulated body not labelled:\n%s", message.Body)
		}
	}
}

func TestCutOffFoundAtResetWhenMissingInitially(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.root, "proj-a", fixtureSessionID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// First sweep sees the limit but no cut-off session.
	h.step(t)
	if len(h.sender.sent) != 1 {
		t.Fatalf("want the limit email first: %v", h.subjects())
	}
	if !strings.Contains(h.sender.sent[0].Body, "search again") {
		t.Fatalf("limit email should promise a retry:\n%s", h.sender.sent[0].Body)
	}

	// The session log appears after the first check, inside the window.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if h.runner.calls != 1 {
		t.Fatalf("reset-time search should resume the session, got %d runs", h.runner.calls)
	}
}

func copyCodexFixture(t *testing.T, root string) {
	t.Helper()
	source := filepath.Join("..", "session", "testdata", "codex", "cutoff", "2026", "10", "26", "rollout-2026-10-26T09-50-00-01a17756-0de8-7fd1-87b3-57ef7c572450.jsonl")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read codex fixture: %v", err)
	}
	target := filepath.Join(root, "2026", "10", "26")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "rollout-2026-10-26T09-50-00-01a17756-0de8-7fd1-87b3-57ef7c572450.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStuckIncidentDoesNotBlockAnotherProvider(t *testing.T) {
	claudeRoot := t.TempDir()
	codexRoot := t.TempDir()
	copyFixture(t, claudeRoot)
	copyCodexFixture(t, codexRoot)

	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Minute)
	claudeProvider := &fakeProvider{name: "claude", snapshot: quota.Snapshot{
		Provider: "claude", AccountID: "acct", ObservedAt: now.Add(-time.Minute),
		Windows: []quota.Window{{ID: "claude:five_hour", Label: "5-hour", UsedPercent: 100, ResetsAt: reset, Duration: 5 * time.Hour}},
	}}
	codexProvider := &fakeProvider{name: "codex", snapshot: quota.Snapshot{
		Provider: "codex", AccountID: "acct", ObservedAt: now.Add(-time.Minute),
		Windows: []quota.Window{{ID: "codex:primary", Label: "5-hour", UsedPercent: 100, ResetsAt: reset, Duration: 5 * time.Hour}},
	}}
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	decider := &fakeDecider{results: []decision.Result{
		{Action: decision.ActionResume, Reason: "continue", Prompt: "finish it"},
		{Action: decision.ActionDone, Reason: "done"},
	}}
	runner := &fakeRunner{outcome: resume.Outcome{LastMessage: "done"}}
built := &Engine{
		DB: db, Config: config.Defaults(),
		Providers: []quota.Provider{claudeProvider},
		Scanner:   session.Scanner{ClaudeRoot: claudeRoot, CodexRoot: codexRoot, MaxBytes: 8 << 20},
		Decider:   decider, Runner: runner, Sender: &fakeSender{},
		Log: logx.Discard(), Now: func() time.Time { return now }, Synchronous: true,
	}
	if err := built.Step(context.Background()); err != nil {
		t.Fatalf("first step: %v", err)
	}
	// The Codex incident is detected a minute later, so Claude is the oldest due one.
	now = now.Add(time.Minute)
	built.Providers = []quota.Provider{claudeProvider, codexProvider}
	if err := built.Step(context.Background()); err != nil {
		t.Fatalf("second step: %v", err)
	}

	// At reset Claude is still exhausted; only Codex can confirm.
	now = now.Add(3 * time.Minute)
	claudeProvider.snapshot.ObservedAt = now
	claudeProvider.snapshot.Windows[0].ResetsAt = now.Add(5 * time.Hour)
	codexProvider.snapshot.ObservedAt = now
	allowed := true
	codexProvider.snapshot.OrdinaryUsageAllowed = &allowed
	codexProvider.snapshot.Windows[0].UsedPercent = 0
	codexProvider.snapshot.Windows[0].ResetsAt = now.Add(5 * time.Hour)
	if err := built.Step(context.Background()); err != nil {
		t.Fatalf("second step: %v", err)
	}

	if runner.calls != 1 {
		t.Fatalf("codex should resume despite the stuck claude incident, calls=%d", runner.calls)
	}
	if runner.specs[0].Provider != "codex" {
		t.Fatalf("wrong provider resumed: %s", runner.specs[0].Provider)
	}
}

func TestUnconfirmedResetExpiresAfter24Hours(t *testing.T) {
	h := newHarness(t)
	h.step(t)
	h.engine.Now = func() time.Time { return h.now.Add(25 * time.Hour) }
	h.provider.snapshot.ObservedAt = h.now.Add(25 * time.Hour)
	h.step(t)

	incidents, _ := h.db.Incidents(context.Background())
	if incidents[0].State != store.IncidentBlocked {
		t.Fatalf("incident should be blocked after 24h: %+v", incidents[0])
	}
	last := h.sender.sent[len(h.sender.sent)-1]
	if !strings.Contains(last.Body, "24 hours") {
		t.Fatalf("expected a 24-hour reason:\n%s", last.Body)
	}
}

func TestWatcherSkipsSimulatedIncidents(t *testing.T) {
	h := newHarness(t)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	incident := store.Incident{
		Provider: "claude", AccountID: "acct", WindowID: "simulated",
		WindowLabel: "simulated", UsedPercent: 100, ResetAt: h.now.Add(-time.Minute),
		Simulated: true, State: store.IncidentAwaitingReset,
		SessionID: fixtureSessionID, SessionCwd: `C:\work\demo`,
	}
	if _, err := h.db.CreateIncident(context.Background(), &incident); err != nil {
		t.Fatal(err)
	}
	h.step(t)
	if h.runner.calls != 0 {
		t.Fatalf("the watcher must not resume simulated incidents, got %d runs", h.runner.calls)
	}
}
