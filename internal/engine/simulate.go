package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ph03iA/CarryOn/internal/session"
	"github.com/ph03iA/CarryOn/internal/store"
)

// Simulate replays the full pipeline for one session with a fake limit and
// reset. Gemini, the agent run, and email are real.
func (e *Engine) Simulate(ctx context.Context, provider, sessionID string) error {
	found, err := e.locateSession(provider, sessionID)
	if err != nil {
		return err
	}
	incident := store.Incident{
		Provider: found.Provider, AccountID: "simulated", WindowID: "simulated",
		WindowLabel: "simulated", UsedPercent: 100,
		ResetAt:   e.now().Add(-time.Second),
		Simulated: true, State: store.IncidentAwaitingReset,
		SessionID: found.ID, SessionPath: found.Path, SessionCwd: found.Cwd,
		CutOffAt: found.CutOffAt, Reason: "simulated limit",
	}
	created, err := e.DB.CreateIncident(ctx, &incident)
	if err != nil {
		return err
	}
	if !created {
		return fmt.Errorf("a simulated incident for this session already exists")
	}
	if _, err := e.DB.CreateResume(ctx, &store.Resume{
		IncidentID: incident.ID, Provider: found.Provider,
		SessionID: found.ID, Cwd: found.Cwd, Simulated: true,
	}); err != nil {
		return err
	}
	e.logf("SIMULATED limit for %s session %s", found.Provider, found.ID)
	runErr := e.runCycle(ctx, incident)
	// Flush whatever the cycle queued, including skip and blocked emails.
	if flushErr := e.deliverOutbox(ctx); flushErr != nil {
		return errors.Join(runErr, flushErr)
	}
	return runErr
}

// locateSession finds a session by id, trying both providers when unspecified.
func (e *Engine) locateSession(provider, sessionID string) (session.Session, error) {
	if provider != "" {
		return e.Scanner.FindByID(provider, sessionID)
	}
	if found, err := e.Scanner.FindByID(session.Claude, sessionID); err == nil {
		return found, nil
	}
	return e.Scanner.FindByID(session.Codex, sessionID)
}
