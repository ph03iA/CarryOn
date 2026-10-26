package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateIncidentIsIdempotent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	reset := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	incident := Incident{
		Provider: "codex", AccountID: "acct", WindowID: "primary",
		WindowLabel: "5-hour", UsedPercent: 100, ResetAt: reset,
	}

	created, err := db.CreateIncident(ctx, &incident)
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	created, err = db.CreateIncident(ctx, &incident)
	if err != nil || created {
		t.Fatalf("duplicate create must be ignored: created=%v err=%v", created, err)
	}

	incidents, err := db.Incidents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 1 {
		t.Fatalf("want 1 incident, got %d", len(incidents))
	}
}

func TestClaimResumeConsumesOneAttempt(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	resume := Resume{IncidentID: "i1", Provider: "claude", SessionID: "s1", Cwd: `C:\work`}
	if _, err := db.CreateResume(ctx, &resume); err != nil {
		t.Fatal(err)
	}

	claimed, err := db.ClaimResume(ctx, resume.ID, 2, "go on")
	if err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v", claimed, err)
	}
	claimed, err = db.ClaimResume(ctx, resume.ID, 2, "go on")
	if err != nil || claimed {
		t.Fatalf("second claim must fail while claimed: claimed=%v err=%v", claimed, err)
	}

	if err := db.StartResume(ctx, resume.ID, "d1"); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishResume(ctx, resume.ID, 0, "done", "a.go"); err != nil {
		t.Fatal(err)
	}
	stored, err := db.Resume(ctx, resume.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != ResumeFinished || stored.Attempts != 1 {
		t.Fatalf("want finished/1, got %s/%d", stored.State, stored.Attempts)
	}
}

func TestClaimResumeStopsAtBudget(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	resume := Resume{IncidentID: "i1", Provider: "codex", SessionID: "s2", Cwd: `C:\work`}
	if _, err := db.CreateResume(ctx, &resume); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if claimed, err := db.ClaimResume(ctx, resume.ID, 2, "go on"); err != nil || !claimed {
			t.Fatalf("attempt %d: claimed=%v err=%v", attempt, claimed, err)
		}
		if err := db.StartResume(ctx, resume.ID, "d"); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishResume(ctx, resume.ID, 0, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := db.ClaimResume(ctx, resume.ID, 2, "go on")
	if err != nil || claimed {
		t.Fatalf("third claim must be refused: claimed=%v err=%v", claimed, err)
	}
}

func TestEnqueueEmailIsIdempotent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	email := Email{EventKey: "limit:abc", Kind: "limit_hit", Subject: "s", Body: "b"}

	if created, err := db.EnqueueEmail(ctx, email); err != nil || !created {
		t.Fatalf("first enqueue: created=%v err=%v", created, err)
	}
	if created, err := db.EnqueueEmail(ctx, email); err != nil || created {
		t.Fatalf("duplicate enqueue: created=%v err=%v", created, err)
	}
}

func TestClaimEmailLeasesAndReleases(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	email := Email{EventKey: "k1", Kind: "k", Subject: "s", Body: "b", NextAttempt: now}
	if _, err := db.EnqueueEmail(ctx, email); err != nil {
		t.Fatal(err)
	}

	claimed, err := db.ClaimEmail(ctx, now, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Attempts != 1 || claimed.State != EmailSending {
		t.Fatalf("unexpected claim: %+v", claimed)
	}
	if _, err := db.ClaimEmail(ctx, now, time.Minute); !errors.Is(err, ErrNoEmail) {
		t.Fatalf("second claim must find nothing, got %v", err)
	}
	if released, err := db.ReleaseStaleLeases(ctx, now.Add(2*time.Minute)); err != nil || released != 1 {
		t.Fatalf("release: released=%d err=%v", released, err)
	}
	if _, err := db.ClaimEmail(ctx, now.Add(2*time.Minute), time.Minute); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
}

func TestReconcileInterruptedMarksRunningRuns(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	resume := Resume{IncidentID: "i1", Provider: "codex", SessionID: "s4", Cwd: `C:\a`}
	if _, err := db.CreateResume(ctx, &resume); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimResume(ctx, resume.ID, 2, "p"); err != nil {
		t.Fatal(err)
	}
	if err := db.StartResume(ctx, resume.ID, "d"); err != nil {
		t.Fatal(err)
	}

	count, err := db.ReconcileInterrupted(ctx)
	if err != nil || count != 1 {
		t.Fatalf("reconcile: count=%d err=%v", count, err)
	}
	stored, err := db.Resume(ctx, resume.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != ResumeInterrupted || stored.Attempts != 1 {
		t.Fatalf("want interrupted/1, got %s/%d", stored.State, stored.Attempts)
	}
}

func TestResumeIsUniquePerIncident(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	first := Resume{IncidentID: "i1", Provider: "claude", SessionID: "same", Cwd: `C:\a`}
	second := Resume{IncidentID: "i2", Provider: "claude", SessionID: "same", Cwd: `C:\a`}

	if created, err := db.CreateResume(ctx, &first); err != nil || !created {
		t.Fatalf("first: created=%v err=%v", created, err)
	}
	if created, err := db.CreateResume(ctx, &second); err != nil || !created {
		t.Fatalf("a new incident must be able to resume the same session: created=%v err=%v", created, err)
	}
	duplicate := Resume{IncidentID: "i1", Provider: "claude", SessionID: "same", Cwd: `C:\a`}
	if created, err := db.CreateResume(ctx, &duplicate); err != nil || created {
		t.Fatalf("duplicate incident resume must be ignored: created=%v err=%v", created, err)
	}
}
