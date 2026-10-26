// Package engine runs the poll loop and the incident state machine.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
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

// Runner continues a session. resume.Runner is the production implementation.
type Runner interface {
	Run(ctx context.Context, spec resume.Spec) (resume.Outcome, error)
}

// Engine ties the providers, the store, decisions, resumes and email together.
type Engine struct {
	DB        *store.DB
	Config    config.Config
	Providers []quota.Provider
	Scanner   session.Scanner
	Decider   decision.Decider
	Runner    Runner
	Sender    mail.Sender
	Log       *logx.Logger
	Now       func() time.Time

	// Synchronous runs resumes inline, which tests rely on.
	Synchronous bool

	mu      sync.Mutex
	running bool

	lastOutcome resume.Outcome
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Engine) logf(format string, args ...any) {
	if e.Log != nil {
		e.Log.Infof(format, args...)
	}
}

func (e *Engine) logErr(format string, args ...any) {
	if e.Log != nil {
		e.Log.Errorf(format, args...)
	}
}

// Step performs one poll, resumes at most one session, and flushes the outbox.
func (e *Engine) Step(ctx context.Context) error {
	var problems []error
	problems = append(problems, e.poll(ctx)...)
	problems = append(problems, e.advance(ctx)...)
	problems = append(problems, e.deliverOutbox(ctx))
	return errors.Join(problems...)
}

// Run loops until the context is cancelled.
func (e *Engine) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Duration(e.Config.PollSeconds) * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	if err := e.Step(ctx); err != nil {
		e.logErr("initial cycle: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := e.Step(ctx); err != nil {
				e.logErr("cycle: %v", err)
			}
		}
	}
}

// poll reads every provider and records exhausted windows.
func (e *Engine) poll(ctx context.Context) []error {
	var problems []error
	for _, provider := range e.Providers {
		snapshot, err := provider.Read(ctx)
		if err != nil {
			e.logErr("%s usage read failed: %v", provider.Name(), err)
			problems = append(problems, err)
			continue
		}
		if err := e.saveSnapshot(ctx, snapshot); err != nil {
			problems = append(problems, err)
			continue
		}
		problems = append(problems, e.recordExhausted(ctx, snapshot)...)
	}
	return problems
}

func (e *Engine) saveSnapshot(ctx context.Context, snapshot quota.Snapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return e.DB.SaveSnapshot(ctx, store.Snapshot{
		Provider: snapshot.Provider, Data: string(data), ObservedAt: snapshot.ObservedAt,
	})
}

// recordExhausted creates one incident per exhausted window.
func (e *Engine) recordExhausted(ctx context.Context, snapshot quota.Snapshot) []error {
	var problems []error
	for _, window := range snapshot.Windows {
		if window.UsedPercent < 100 {
			continue
		}
		incident := store.Incident{
			Provider: snapshot.Provider, AccountID: snapshot.AccountID,
			WindowID: window.ID, WindowLabel: window.Label,
			UsedPercent: window.UsedPercent, ResetAt: window.ResetsAt,
			WindowDuration: window.Duration,
		}
		created, err := e.DB.CreateIncident(ctx, &incident)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !created {
			continue
		}
		e.logf("%s %s limit exhausted; resets %s", snapshot.Provider, window.Label, window.ResetsAt)
		problems = append(problems, e.announceIncident(ctx, incident))
	}
	return problems
}

// announceIncident finds the cut-off session and records it. It sends no email:
// CarryOn only emails when the limit comes back.
func (e *Engine) announceIncident(ctx context.Context, incident store.Incident) error {
	update := store.IncidentUpdate{State: store.IncidentAwaitingReset}
	found, ok := e.findCutOff(incident)
	if ok {
		update.SessionID = found.ID
		update.SessionPath = found.Path
		update.SessionCwd = found.Cwd
		update.CutOffAt = found.CutOffAt
		update.CutOffOffset = found.ResumeFrom
		update.Reason = "cut-off session found"
		if _, err := e.DB.CreateResume(ctx, &store.Resume{
			IncidentID: incident.ID, Provider: incident.Provider,
			SessionID: found.ID, Cwd: found.Cwd, Simulated: incident.Simulated,
		}); err != nil {
			return err
		}
		e.logf("cut-off session for %s: %s in %s", incident.Provider, found.ID, found.Cwd)
	} else {
		update.Reason = "no cut-off session found yet"
		e.logf("no cut-off session for %s yet; will search again at reset", incident.Provider)
	}
	return e.DB.UpdateIncident(ctx, incident.ID, update)
}

// advance starts the next due resume, in the background unless synchronous.
// provider returns the reader for a provider name.
func (e *Engine) provider(name string) quota.Provider {
	for _, provider := range e.Providers {
		if provider.Name() == name {
			return provider
		}
	}
	return nil
}

// enqueue renders and stores one email event.
func (e *Engine) enqueue(ctx context.Context, eventKey string, event mail.Event) error {
	subject, body := mail.Render(event)
	created, err := e.DB.EnqueueEmail(ctx, store.Email{
		EventKey: eventKey, Kind: event.Kind, Subject: subject, Body: body,
		Simulated: event.Simulated, NextAttempt: e.now(),
	})
	if err != nil {
		return err
	}
	if created {
		e.logf("queued email %s (%s)", event.Kind, eventKey)
	}
	return nil
}

// maxAttempts bounds delivery retries for one email.
const maxAttempts = 8

// deliverOutbox sends queued emails, retrying transient failures.
func (e *Engine) deliverOutbox(ctx context.Context) error {
	if e.Sender == nil {
		return nil
	}
	// Recover emails whose delivery lease expired after a crash or a slow send.
	if _, err := e.DB.ReleaseStaleLeases(ctx, e.now()); err != nil {
		return err
	}
	for range 20 {
		email, err := e.DB.ClaimEmail(ctx, e.now(), 2*time.Minute)
		if errors.Is(err, store.ErrNoEmail) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := e.sendEmail(ctx, email); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) sendEmail(ctx context.Context, email store.Email) error {
	message := mail.Message{
		EventKey: email.EventKey, To: e.Config.Recipient,
		From: e.Config.Sender, Subject: email.Subject, Body: email.Body,
	}
	sendErr := e.Sender.Send(ctx, message)
	if sendErr == nil {
		e.logf("email sent: %s", email.Kind)
		return e.DB.FinishEmail(ctx, email.ID, store.EmailSent, "", e.now())
	}
	state, next := retryDecision(email, sendErr, e.now())
	e.logErr("email %s: %s (%s)", email.Kind, sendErr, state)
	return e.DB.FinishEmail(ctx, email.ID, state, sendErr.Error(), next)
}

func retryDecision(email store.Email, sendErr error, now time.Time) (string, time.Time) {
	if !mail.Retryable(sendErr) || email.Attempts >= maxAttempts {
		return store.EmailFailed, now
	}
	delay := time.Minute * time.Duration(1<<min(email.Attempts, 6))
	if requested := mail.RetryAfter(sendErr); requested > delay {
		delay = requested
	}
	return store.EmailRetry, now.Add(delay)
}

// Reconcile repairs state left behind by a crash.
func (e *Engine) Reconcile(ctx context.Context) error {
	interrupted, err := e.DB.ReconcileInterrupted(ctx)
	if err != nil {
		return err
	}
	if interrupted > 0 {
		e.logf("%d interrupted resume(s) marked; they need a new decision", interrupted)
	}
	if _, err := e.DB.ResetInFlightIncidents(ctx); err != nil {
		return err
	}
	return nil
}

// Status is a human-readable snapshot of CarryOn's state.
type Status struct {
	Incidents []store.Incident
	Resumes   []store.Resume
	Emails    []store.Email
	Decisions []store.Decision
}

// LoadStatus reads the recent state for the status command.
func (e *Engine) LoadStatus(ctx context.Context) (Status, error) {
	incidents, err := e.DB.Incidents(ctx)
	if err != nil {
		return Status{}, err
	}
	resumes, err := e.DB.Resumes(ctx, 20)
	if err != nil {
		return Status{}, err
	}
	emails, err := e.DB.Emails(ctx, 20)
	if err != nil {
		return Status{}, err
	}
	decisions, err := e.DB.Decisions(ctx, 20)
	if err != nil {
		return Status{}, err
	}
	return Status{Incidents: incidents, Resumes: resumes, Emails: emails, Decisions: decisions}, nil
}

// findCutOff searches for a session cut off inside the incident's window.
func (e *Engine) findCutOff(incident store.Incident) (session.Session, bool) {
	since := time.Time{}
	if incident.WindowDuration > 0 {
		since = incident.ResetAt.Add(-incident.WindowDuration)
	}
	found, ok, err := e.Scanner.FindCutOff(incident.Provider, since)
	if err != nil {
		e.logErr("scan %s sessions: %v", incident.Provider, err)
		return session.Session{}, false
	}
	return found, ok
}

// attachCutOffSession retries the search at reset time.
func (e *Engine) attachCutOffSession(ctx context.Context, incident store.Incident) (store.Incident, error) {
	found, ok := e.findCutOff(incident)
	if !ok {
		return incident, errors.New("no cut-off session was found in this window")
	}
	incident.SessionID = found.ID
	incident.SessionPath = found.Path
	incident.SessionCwd = found.Cwd
	incident.CutOffAt = found.CutOffAt
	incident.CutOffOffset = found.ResumeFrom
	if _, err := e.DB.CreateResume(ctx, &store.Resume{
		IncidentID: incident.ID, Provider: incident.Provider,
		SessionID: found.ID, Cwd: found.Cwd, Simulated: incident.Simulated,
	}); err != nil {
		return incident, err
	}
	if err := e.DB.UpdateIncident(ctx, incident.ID, incidentUpdate(incident, store.IncidentAwaitingReset, "cut-off session found at reset")); err != nil {
		return incident, err
	}
	e.logf("found cut-off session %s at reset", found.ID)
	return incident, nil
}

// sendBack reports that the limit is back and what CarryOn did about it.
func (e *Engine) sendBack(ctx context.Context, incident store.Incident, summary, prompt, reason string) error {
	return e.enqueue(ctx, "back:"+incident.ID, mail.Event{
		Kind: mail.KindBack, Provider: incident.Provider, Summary: summary,
		ResetAt: incident.ResetAt, SessionID: incident.SessionID, SessionCwd: incident.SessionCwd,
		Prompt: prompt, Reason: reason, Simulated: incident.Simulated, Now: e.now(),
	})
}

// sendNeedsYou reports a reset that was never confirmed.
func (e *Engine) sendNeedsYou(ctx context.Context, incident store.Incident) error {
	return e.enqueue(ctx, "needsyou:"+incident.ID, mail.Event{
		Kind: mail.KindNeedsYou, Provider: incident.Provider, WindowLabel: incident.WindowLabel,
		ResetAt: incident.ResetAt, SessionID: incident.SessionID, SessionCwd: incident.SessionCwd,
		Simulated: incident.Simulated, Now: e.now(),
	})
}

// RunOnce performs one cycle and waits for any resume it starts, so a --once
// command does not exit and kill the agent.
func (e *Engine) RunOnce(ctx context.Context) error {
	previous := e.Synchronous
	e.Synchronous = true
	defer func() { e.Synchronous = previous }()
	return e.Step(ctx)
}
