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
	results  []decision.Result
	errs     []error
	calls    int
	last     decision.Request
	requests []decision.Request
}

func (f *fakeDecider) Decide(_ context.Context, request decision.Request) (decision.Result, error) {
	f.last = request
	f.requests = append(f.requests, request)
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
	release chan struct{}
}

func (f *fakeRunner) Run(_ context.Context, spec resume.Spec) (resume.Outcome, error) {
	if f.release != nil {
		<-f.release
	}
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

func TestLimitHitSendsNoEmailAndRecordsSession(t *testing.T) {
	h := newHarness(t)
	h.step(t)
	h.step(t)

	if len(h.sender.sent) != 0 {
		t.Fatalf("a limit hit must not email, got %v", h.subjects())
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
	if len(h.sender.sent) != 0 {
		t.Fatalf("a limit hit must not email: %v", h.subjects())
	}

	// The reset happens and a fresh reading confirms it.
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if countBack(h.sender.sent) != 1 {
		t.Fatalf("want exactly one back email, got %v", h.subjects())
	}
	if h.runner.calls != 1 {
		t.Fatalf("want one resume run, got %d", h.runner.calls)
	}
	if h.runner.specs[0].SessionID != fixtureSessionID {
		t.Fatalf("resume must target the session id, got %+v", h.runner.specs[0])
	}
	if !strings.HasPrefix(h.runner.specs[0].Prompt, decision.FixedInstruction) {
		t.Fatal("resume prompt must start with the fixed instruction")
	}

	combined := strings.Join(h.subjects(), "\n")
	for _, message := range h.sender.sent {
		combined += "\n" + message.Body
	}
	for _, want := range []string{"is back", "resuming your session", "finished"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("missing %q in emails:\n%s", want, combined)
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

func TestCrashUsesThePostRunDecision(t *testing.T) {
	h := newHarness(t)
	h.step(t)

	incidents, _ := h.db.Incidents(context.Background())
	incident := incidents[0]
	resumeRow, err := h.db.ResumeForIncident(context.Background(), "claude", fixtureSessionID, incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A crashed run: claimed, running, and it wrote to the session log.
	if _, err := h.db.ClaimResume(context.Background(), resumeRow.ID, 2, "first prompt", "d1"); err != nil {
		t.Fatal(err)
	}
	appendSessionEvent(t, h.root, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Edited retry.go before the crash."}]},"uuid":"e0000000-0000-4000-8000-000000000009","timestamp":"2026-10-26T12:01:00.000Z","cwd":"C:\\work\\demo"}`)
	if err := h.db.UpdateIncident(context.Background(), incident.ID, store.IncidentUpdate{
		State: store.IncidentResuming, SessionID: incident.SessionID,
		SessionPath: incident.SessionPath, SessionCwd: incident.SessionCwd,
	}); err != nil {
		t.Fatal(err)
	}

	restarted, _ := newEngine(t, h.dbPath, h.root, h.provider, h.now)
	restarted.Decider.(*fakeDecider).results = []decision.Result{
		{Action: decision.ActionDone, Reason: "the interrupted run had already finished"},
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
		t.Fatalf("the post-run decision said done, got %d runs", calls)
	}
	stored, err := restarted.DB.Incidents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stored[0].State != store.IncidentFinished {
		t.Fatalf("incident should be finished: %+v", stored[0])
	}
	messages := restarted.Sender.(*fakeSender).sent
	if len(messages) == 0 {
		t.Fatal("expected a finish email")
	}
	last := messages[len(messages)-1]
	if strings.Contains(last.Body, "already continued") {
		t.Fatalf("crash was misreported as an external continuation:\n%s", last.Body)
	}
	if !strings.Contains(last.Body, "interrupted run had already finished") {
		t.Fatalf("finish email missing the post-run decision:\n%s", last.Body)
	}
	decisions, err := restarted.DB.Decisions(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) == 0 || decisions[0].Stage != "post" || decisions[0].Action != decision.ActionDone {
		t.Fatalf("expected a post-run decision: %+v", decisions)
	}
}

func appendSessionEvent(t *testing.T, root, line string) {
	t.Helper()
	path := filepath.Join(root, "proj-a", fixtureSessionID+".jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
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
	if countBack(h.sender.sent) != 1 || len(h.sender.sent) != 1 {
		t.Fatalf("want exactly one back email, got %v", h.subjects())
	}
	last := h.sender.sent[len(h.sender.sent)-1]
	if !strings.Contains(last.Body, "skipped: no Gemini decision") {
		t.Fatalf("expected a no-decision back email:\n%s", last.Body)
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
	if countBack(h.sender.sent) != 1 || len(h.sender.sent) != 1 {
		t.Fatalf("want exactly one back email, got %v", h.subjects())
	}
	last := h.sender.sent[len(h.sender.sent)-1]
	if !strings.Contains(last.Body, "skipped: the session was already continued") {
		t.Fatalf("expected an already-continued back email:\n%s", last.Body)
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
	if len(h.sender.sent) != 0 {
		t.Fatalf("a limit hit must not email: %v", h.subjects())
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

func TestNewIncidentCanResumeTheSameSessionAgain(t *testing.T) {
	h := newHarness(t)
	h.decider.results = []decision.Result{
		{Action: decision.ActionResume, Reason: "work remains", Prompt: "first pass"},
		{Action: decision.ActionResumeAgain, Reason: "one more step", Prompt: "second pass"},
		{Action: decision.ActionDone, Reason: "first task done"},
		{Action: decision.ActionResume, Reason: "new incident", Prompt: "third pass"},
		{Action: decision.ActionDone, Reason: "second task done"},
	}

	h.step(t)
	firstReset := h.now.Add(3 * time.Minute)
	h.engine.Now = func() time.Time { return firstReset }
	h.provider.snapshot.ObservedAt = firstReset
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)
	if h.runner.calls != 2 {
		t.Fatalf("first incident should use both attempts, got %d", h.runner.calls)
	}

	// A new limit window cuts the same session off again.
	secondSweep := h.now.Add(time.Hour)
	h.engine.Now = func() time.Time { return secondSweep }
	h.provider.snapshot.ObservedAt = secondSweep
	h.provider.snapshot.Windows[0].UsedPercent = 100
	h.provider.snapshot.Windows[0].ResetsAt = secondSweep.Add(2 * time.Minute)
	h.step(t)

	secondReset := secondSweep.Add(3 * time.Minute)
	h.engine.Now = func() time.Time { return secondReset }
	h.provider.snapshot.ObservedAt = secondReset
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = secondReset.Add(5 * time.Hour)
	h.step(t)

	if h.runner.calls != 3 {
		t.Fatalf("a new incident must get its own resume budget, got %d calls", h.runner.calls)
	}
}

type blockingDecider struct{}

func (blockingDecider) Decide(ctx context.Context, _ decision.Request) (decision.Result, error) {
	<-ctx.Done()
	return decision.Result{}, ctx.Err()
}

func TestDecideIsBounded(t *testing.T) {
	previous := decideTimeout
	decideTimeout = 100 * time.Millisecond
	defer func() { decideTimeout = previous }()

	built := &Engine{Decider: blockingDecider{}}
	started := time.Now()
	if _, err := built.decide(context.Background(), decision.Request{}); err == nil {
		t.Fatal("want a deadline error")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("decide ignored its timeout for %s", elapsed)
	}
}

func TestDecisionCarriesAttemptCount(t *testing.T) {
	h := newHarness(t)
	h.decider.results = []decision.Result{
		{Action: decision.ActionResume, Reason: "start", Prompt: "first"},
		{Action: decision.ActionResumeAgain, Reason: "more", Prompt: "second"},
		{Action: decision.ActionDone, Reason: "finished"},
	}
	h.step(t)
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	for _, request := range h.decider.requests {
		if request.Attempts > 0 {
			return
		}
	}
	t.Fatalf("no decision carried the attempt count: %+v", h.decider.requests)
}

func countBack(messages []mail.Message) int {
	count := 0
	for _, message := range messages {
		if strings.Contains(message.Subject, "is back") {
			count++
		}
	}
	return count
}

func TestConfirmedResetSendsOneBackEmailWhenNoSession(t *testing.T) {
	h := newHarness(t)
	os.Remove(filepath.Join(h.root, "proj-a", fixtureSessionID+".jsonl"))
	h.step(t)
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if countBack(h.sender.sent) != 1 || len(h.sender.sent) != 1 {
		t.Fatalf("want exactly one back email, got %v", h.subjects())
	}
	if !strings.Contains(h.sender.sent[0].Body, "nothing to resume") {
		t.Fatalf("unexpected back email:\n%s", h.sender.sent[0].Body)
	}
	if h.runner.calls != 0 {
		t.Fatal("nothing should resume")
	}
}

func TestConfirmedResetSendsOneBackEmailWhenDone(t *testing.T) {
	h := newHarness(t)
	h.decider.results = []decision.Result{{Action: decision.ActionSkipDone, Reason: "the task was complete"}}
	h.step(t)
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if countBack(h.sender.sent) != 1 || len(h.sender.sent) != 1 {
		t.Fatalf("want exactly one back email, got %v", h.subjects())
	}
	if !strings.Contains(h.sender.sent[0].Body, "skipped: the task already looked done") {
		t.Fatalf("unexpected back email:\n%s", h.sender.sent[0].Body)
	}
}

func TestConfirmedResetSendsOneBackEmailWhenStuck(t *testing.T) {
	h := newHarness(t)
	h.decider.results = []decision.Result{{Action: decision.ActionSkipStuck, Reason: "the agent was looping"}}
	h.step(t)
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if countBack(h.sender.sent) != 1 || len(h.sender.sent) != 1 {
		t.Fatalf("want exactly one back email, got %v", h.subjects())
	}
	if !strings.Contains(h.sender.sent[0].Body, "skipped: the agent looked stuck") {
		t.Fatalf("unexpected back email:\n%s", h.sender.sent[0].Body)
	}
}

func TestUnconfirmedResetSendsNothingUntil24Hours(t *testing.T) {
	h := newHarness(t)
	h.step(t)
	if len(h.sender.sent) != 0 {
		t.Fatalf("a limit hit must not email: %v", h.subjects())
	}

	// The reset time passes but the limit is still exhausted.
	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(10 * time.Hour)
	h.step(t)
	if len(h.sender.sent) != 0 {
		t.Fatalf("an unconfirmed reset must not email: %v", h.subjects())
	}

	h.engine.Now = func() time.Time { return h.now.Add(25 * time.Hour) }
	h.step(t)
	if len(h.sender.sent) != 1 {
		t.Fatalf("want one needs-you email, got %v", h.subjects())
	}
	if !strings.Contains(h.sender.sent[0].Subject, "needs you") || !strings.Contains(h.sender.sent[0].Body, "24 hours") {
		t.Fatalf("unexpected expiry email: %s\n%s", h.sender.sent[0].Subject, h.sender.sent[0].Body)
	}
}

func TestEnqueueUsesEngineClock(t *testing.T) {
	h := newHarness(t)
	engineNow := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	h.engine.Now = func() time.Time { return engineNow }
	if err := h.engine.enqueue(context.Background(), "clock:test", mail.Event{
		Kind: mail.KindBack, Provider: "claude", Summary: "nothing to resume",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.ClaimEmail(context.Background(), engineNow, time.Minute); err != nil {
		t.Fatalf("email stamped with the real clock is not due: %v", err)
	}
}

func TestResumeStartFailureSendsFinishEmail(t *testing.T) {
	h := newHarness(t)
	h.runner.err = errors.New("spawn failed")
	h.step(t)

	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)
	h.step(t)

	if len(h.sender.sent) != 2 {
		t.Fatalf("want a back email and a finish email, got %v", h.subjects())
	}
	last := h.sender.sent[len(h.sender.sent)-1]
	if !strings.Contains(last.Subject, "finished") {
		t.Fatalf("expected a finish email, got %q", last.Subject)
	}
	if !strings.Contains(last.Body, "could not start") || !strings.Contains(last.Body, "spawn failed") {
		t.Fatalf("finish email missing the start failure:\n%s", last.Body)
	}
}

func TestRunOnceWaitsForResume(t *testing.T) {
	h := newHarness(t)
	h.engine.Synchronous = false
	release := make(chan struct{})
	h.runner.release = release
	h.step(t)

	h.engine.Now = func() time.Time { return h.now.Add(3 * time.Minute) }
	h.provider.snapshot.ObservedAt = h.now.Add(3 * time.Minute)
	h.provider.snapshot.Windows[0].UsedPercent = 0
	h.provider.snapshot.Windows[0].ResetsAt = h.now.Add(5 * time.Hour)

	done := make(chan error, 1)
	go func() { done <- h.engine.RunOnce(context.Background()) }()
	select {
	case <-done:
		t.Fatal("RunOnce returned before the resume finished")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunOnce did not return after the resume finished")
	}
}
