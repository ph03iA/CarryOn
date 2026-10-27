package engine

import (
	"context"
	"time"

	"github.com/ph03iA/CarryOn/internal/decision"
	"github.com/ph03iA/CarryOn/internal/session"
	"github.com/ph03iA/CarryOn/internal/store"
)

// advance tries every due incident until one passes reset confirmation.
func (e *Engine) advance(ctx context.Context) []error {
	e.mu.Lock()
	busy := e.running
	e.mu.Unlock()
	if busy {
		return nil
	}
	incidents, err := e.DB.Incidents(ctx, store.IncidentAwaitingReset)
	if err != nil {
		return []error{err}
	}
	var problems []error
	for _, incident := range incidents {
		if incident.Simulated {
			continue
		}
		if !e.Config.Watches(incident.Provider) {
			problems = append(problems, e.closeIncident(ctx, incident, store.IncidentSkipped, "provider disabled"))
			continue
		}
		if incident.ResetAt.After(e.now()) {
			continue
		}
		if e.now().Sub(incident.ResetAt) > 24*time.Hour {
			_ = e.sendNeedsYou(ctx, incident)
			problems = append(problems, e.closeIncident(ctx, incident, store.IncidentBlocked,
				"the reset was not confirmed within 24 hours"))
			continue
		}
		prepared, found, ok, err := e.prepareCycle(ctx, incident)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !ok {
			continue
		}
		problems = append(problems, e.startRun(ctx, prepared, found))
		return problems
	}
	return problems
}

// startRun launches the decision and resume work for a prepared incident.
func (e *Engine) startRun(ctx context.Context, incident store.Incident, found session.Session) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil
	}
	e.running = true
	e.mu.Unlock()

	if e.Synchronous {
		defer e.finishRun()
		return e.decideAndResume(ctx, incident, found)
	}
	go func() {
		defer e.finishRun()
		if err := e.decideAndResume(context.WithoutCancel(ctx), incident, found); err != nil {
			e.logErr("resume cycle: %v", err)
		}
	}()
	return nil
}
func (e *Engine) finishRun() {
	e.mu.Lock()
	e.running = false
	e.mu.Unlock()
}

// decideTimeout bounds one Gemini call. Tests shorten it.
var decideTimeout = 2 * time.Minute

// decide wraps every Gemini call in its own deadline.
func (e *Engine) decide(ctx context.Context, request decision.Request) (decision.Result, error) {
	decideCtx, cancel := context.WithTimeout(ctx, decideTimeout)
	defer cancel()
	return e.Decider.Decide(decideCtx, request)
}
