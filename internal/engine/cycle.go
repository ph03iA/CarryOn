package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ph03iA/CarryOn/internal/decision"
	"github.com/ph03iA/CarryOn/internal/mail"
	"github.com/ph03iA/CarryOn/internal/quota"
	"github.com/ph03iA/CarryOn/internal/resume"
	"github.com/ph03iA/CarryOn/internal/session"
	"github.com/ph03iA/CarryOn/internal/store"
)

// prepareCycle confirms the reset and returns the session to resume. ok is
// false when the reset is not confirmed yet, so another incident can be tried.
func (e *Engine) prepareCycle(ctx context.Context, incident store.Incident) (store.Incident, session.Session, bool, error) {
	if !incident.Simulated {
		confirmed, err := e.confirmReset(ctx, incident)
		if err != nil {
			e.logErr("confirm reset: %v", err)
			return incident, session.Session{}, false, nil
		}
		if !confirmed {
			e.logf("%s reset not confirmed yet; will retry", incident.Provider)
			return incident, session.Session{}, false, nil
		}
	}
	if incident.SessionID == "" {
		attached, err := e.attachCutOffSession(ctx, incident)
		if err != nil {
			_ = e.sendBack(ctx, incident, "nothing to resume", "", err.Error())
			return incident, session.Session{}, false, e.closeIncident(ctx, incident, store.IncidentSkipped, err.Error())
		}
		incident = attached
	}
	found, err := e.sessionFor(incident)
	if err != nil {
		_ = e.sendBack(ctx, incident, "nothing to resume", "", err.Error())
		return incident, session.Session{}, false, e.closeIncident(ctx, incident, store.IncidentSkipped, err.Error())
	}
	if _, interrupted := e.interruptedResume(ctx, incident); !interrupted {
		continued, err := e.Scanner.Continued(found)
		if err != nil {
			_ = e.sendBack(ctx, incident, "skipped: could not read the session log", "", err.Error())
			return incident, session.Session{}, false, e.closeIncident(ctx, incident, store.IncidentBlocked, "could not read the session log: "+err.Error())
		}
		if continued {
			_ = e.sendBack(ctx, incident, "skipped: the session was already continued", "", "")
			return incident, session.Session{}, false, e.skipContinued(ctx, incident, found)
		}
	}
	if err := e.DB.UpdateIncident(ctx, incident.ID, incidentUpdate(incident, store.IncidentDeciding, "reset confirmed")); err != nil {
		return incident, session.Session{}, false, err
	}
	return incident, found, true, nil
}

// runCycle prepares and runs an incident inline. simulate uses it.
func (e *Engine) runCycle(ctx context.Context, incident store.Incident) error {
	prepared, found, ok, err := e.prepareCycle(ctx, incident)
	if err != nil || !ok {
		return err
	}
	return e.decideAndResume(ctx, prepared, found)
}

// sessionFor reloads the session by id, falling back to the stored path.
func (e *Engine) sessionFor(incident store.Incident) (session.Session, error) {
	found, err := e.Scanner.FindByID(incident.Provider, incident.SessionID)
	if err == nil {
		if incident.CutOffOffset > 0 {
			found.ResumeFrom = incident.CutOffOffset
		}
		return found, nil
	}
	if incident.SessionPath == "" {
		return session.Session{}, fmt.Errorf("session %s is no longer on disk", incident.SessionID)
	}
	return session.Session{
		Provider: incident.Provider, ID: incident.SessionID, Path: incident.SessionPath,
		Cwd: incident.SessionCwd, CutOffAt: incident.CutOffAt, ResumeFrom: incident.CutOffOffset,
	}, nil
}

// confirmReset requires a fresh reading after the reset with no limit still hit.
func (e *Engine) confirmReset(ctx context.Context, incident store.Incident) (bool, error) {
	provider := e.provider(incident.Provider)
	if provider == nil {
		return false, fmt.Errorf("no reader for %s", incident.Provider)
	}
	snapshot, err := provider.Read(ctx)
	if err != nil {
		return false, err
	}
	if err := e.saveSnapshot(ctx, snapshot); err != nil {
		return false, err
	}
	return e.resetConfirmed(incident, snapshot, e.now()), nil
}

func (e *Engine) resetConfirmed(incident store.Incident, snapshot quota.Snapshot, now time.Time) bool {
	if snapshot.AccountID != incident.AccountID {
		return false
	}
	if snapshot.ObservedAt.Before(incident.ResetAt) {
		return false
	}
	if now.Sub(snapshot.ObservedAt) > 2*time.Minute {
		return false
	}
	if anyWindowExhausted(snapshot, now) {
		return false
	}
	return usageAllowed(snapshot)
}

func anyWindowExhausted(snapshot quota.Snapshot, now time.Time) bool {
	for _, window := range snapshot.Windows {
		if window.UsedPercent >= 100 && window.ResetsAt.After(now) {
			return true
		}
	}
	return false
}

func usageAllowed(snapshot quota.Snapshot) bool {
	if snapshot.Provider != "codex" {
		return true
	}
	return snapshot.OrdinaryUsageAllowed != nil && *snapshot.OrdinaryUsageAllowed
}

// skipContinued records that something else already continued the session.
func (e *Engine) skipContinued(ctx context.Context, incident store.Incident, found session.Session) error {
	reason := "the session was already continued after the cut-off, so CarryOn left it alone"
	if err := e.DB.UpdateIncident(ctx, incident.ID, incidentUpdate(incident, store.IncidentSkipped, reason)); err != nil {
		return err
	}
	e.logf("skipped %s session %s: %s", incident.Provider, found.ID, reason)
	return nil
}

// decideAndResume asks Gemini, then acts on its answer.
func (e *Engine) decideAndResume(ctx context.Context, incident store.Incident, found session.Session) error {
	if record, interrupted := e.interruptedResume(ctx, incident); interrupted {
		return e.resumeAfterCrash(ctx, incident, found, record)
	}
	transcript := e.Scanner.Transcript(found, e.Config.TailChars)
	result, err := e.decide(ctx, decision.Request{
		Stage: "pre", Provider: incident.Provider, Cwd: found.Cwd,
		SessionID: found.ID, LimitLabel: incident.WindowLabel, ResetAt: incident.ResetAt,
		Attempts: e.attempts(ctx, incident), Transcript: transcript,
		TailChars: e.Config.TailChars, PromptMax: e.Config.PromptChars,
	})
	if err != nil {
		return e.blockOnDecision(ctx, incident, "pre", err)
	}
	if err := e.recordDecision(ctx, incident, "", "pre", result); err != nil {
		return err
	}
	switch result.Action {
	case decision.ActionResume:
		if err := e.sendBack(ctx, incident, "resuming your session in "+found.Cwd, decision.ResumePrompt(result.Prompt), result.Reason); err != nil {
			return err
		}
		return e.resumeLoop(ctx, incident, found, result)
	case decision.ActionSkipDone:
		_ = e.sendBack(ctx, incident, "skipped: the task already looked done", "", result.Reason)
		return e.closeIncident(ctx, incident, store.IncidentSkipped, result.Reason)
	default:
		_ = e.sendBack(ctx, incident, "skipped: the agent looked stuck", "", result.Reason)
		return e.closeIncident(ctx, incident, store.IncidentBlocked, result.Reason)
	}
}

// resumeLoop runs the agent, then asks Gemini after each run.
func (e *Engine) resumeLoop(ctx context.Context, incident store.Incident, found session.Session, result decision.Result) error {
	prompt := decision.ResumePrompt(result.Prompt)
	for {
		done, err := e.runOneAttempt(ctx, incident, found, prompt, result.Reason)
		if err != nil || done {
			return err
		}
		next, err := e.postDecision(ctx, incident, found)
		if err != nil {
			return e.blockOnDecision(ctx, incident, "post", err)
		}
		if next.Action != decision.ActionResumeAgain {
			return e.closeIncident(ctx, incident, store.IncidentFinished, next.Reason)
		}
		prompt = decision.ResumePrompt(next.Prompt)
		result = next
	}
}

// runOneAttempt claims and runs a single resume, reporting whether to stop.
func (e *Engine) runOneAttempt(ctx context.Context, incident store.Incident, found session.Session, prompt, reason string) (bool, error) {
	record, err := e.DB.ResumeForIncident(ctx, incident.Provider, found.ID, incident.ID)
	if err != nil {
		return true, err
	}
	claimed, err := e.DB.ClaimResume(ctx, record.ID, e.Config.MaxResumes, prompt, "")
	if err != nil {
		return true, err
	}
	if !claimed {
		message := fmt.Sprintf("CarryOn already resumed this session %d time(s), so it stopped here", record.Attempts)
		return true, e.closeIncident(ctx, incident, store.IncidentBlocked, message)
	}
	if err := e.DB.UpdateIncident(ctx, incident.ID, incidentUpdate(incident, store.IncidentResuming, reason)); err != nil {
		return true, err
	}
	_ = e.deliverOutbox(ctx)

	outcome, runErr := e.Runner.Run(ctx, resume.Spec{
		Provider: incident.Provider, SessionID: found.ID, Cwd: found.Cwd, Prompt: prompt,
	})
	if runErr != nil {
		if err := e.DB.FailResume(ctx, record.ID, runErr.Error()); err != nil {
			return true, err
		}
		return true, e.closeIncident(ctx, incident, store.IncidentBlocked, "the resume could not start: "+runErr.Error())
	}
	if err := e.DB.FinishResume(ctx, record.ID, outcome.ExitCode, outcome.LastMessage, strings.Join(outcome.Files, ", ")); err != nil {
		return true, err
	}
	e.lastOutcome = outcome
	return false, nil
}

// postDecision asks Gemini what to do after a run.
func (e *Engine) postDecision(ctx context.Context, incident store.Incident, found session.Session) (decision.Result, error) {
	outcome := e.lastOutcome
	record, _ := e.DB.ResumeForIncident(ctx, incident.Provider, found.ID, incident.ID)
	summary := buildRunSummary(outcome)
	result, err := e.decide(ctx, decision.Request{
		Stage: "post", Provider: incident.Provider, Cwd: found.Cwd,
		SessionID: found.ID, LimitLabel: incident.WindowLabel, ResetAt: incident.ResetAt,
		Attempts:   record.Attempts,
		Transcript: e.Scanner.Transcript(found, e.Config.TailChars),
		RunSummary: summary, TailChars: e.Config.TailChars, PromptMax: e.Config.PromptChars,
	})
	if err != nil {
		return decision.Result{}, err
	}
	if err := e.recordDecision(ctx, incident, record.ID, "post", result); err != nil {
		return decision.Result{}, err
	}
	if result.Action == decision.ActionResumeAgain && record.Attempts >= e.Config.MaxResumes {
		result.Action = decision.ActionStop
		result.Reason = fmt.Sprintf("%s (stopped: the %d-resume limit was reached)", result.Reason, e.Config.MaxResumes)
	}
	if err := e.enqueue(ctx, resumeKey(record.ID, "finish", record.Attempts), mail.Event{
		Kind: mail.KindResumeFinish, Provider: incident.Provider, Action: result.Action,
		Reason: result.Reason, SessionID: found.ID, SessionCwd: found.Cwd,
		ExitCode: outcome.ExitCode, TimedOut: outcome.TimedOut,
		LastMessage: outcome.LastMessage, Files: outcome.Files, TestOutput: outcome.TestOutput,
		Simulated: incident.Simulated, Now: e.now(),
	}); err != nil {
		return decision.Result{}, err
	}
	_ = e.deliverOutbox(ctx)
	return result, nil
}

func buildRunSummary(outcome resume.Outcome) string {
	parts := []string{fmt.Sprintf("exit status: %d", outcome.ExitCode)}
	if outcome.TimedOut {
		parts = append(parts, "the run was stopped by the time limit")
	}
	if outcome.DiffStat != "" {
		parts = append(parts, "git diff --stat:\n"+outcome.DiffStat)
	}
	if outcome.DiffExcerpt != "" {
		parts = append(parts, "git diff excerpt:\n"+outcome.DiffExcerpt)
	}
	if outcome.TestOutput != "" {
		parts = append(parts, "test output:\n"+outcome.TestOutput)
	}
	return strings.Join(parts, "\n\n")
}

// blockOnDecision records a fallback decision and reports the failure.
func (e *Engine) blockOnDecision(ctx context.Context, incident store.Incident, stage string, cause error) error {
	reason := "Gemini did not return a usable decision, so CarryOn did not resume: " + cause.Error()
	if err := e.recordDecision(ctx, incident, "", stage, decision.Result{
		Action: decision.ActionError, Reason: reason, Fallback: true,
	}); err != nil {
		return err
	}
	if stage == "pre" {
		_ = e.sendBack(ctx, incident, "skipped: no Gemini decision", "", reason)
	} else {
		e.enqueueFinish(ctx, incident, "no decision", reason)
	}
	return e.closeIncident(ctx, incident, store.IncidentBlocked, reason)
}

// enqueueFinish reports the end of a run that produced no decision.
func (e *Engine) enqueueFinish(ctx context.Context, incident store.Incident, action, reason string) {
	record, _ := e.DB.ResumeForIncident(ctx, incident.Provider, incident.SessionID, incident.ID)
	outcome := e.lastOutcome
	_ = e.enqueue(ctx, resumeKey(record.ID, "finish", record.Attempts), mail.Event{
		Kind: mail.KindResumeFinish, Provider: incident.Provider, Action: action,
		Reason: reason, SessionID: incident.SessionID, SessionCwd: incident.SessionCwd,
		ExitCode: outcome.ExitCode, TimedOut: outcome.TimedOut,
		LastMessage: outcome.LastMessage, Files: outcome.Files, TestOutput: outcome.TestOutput,
		Simulated: incident.Simulated, Now: e.now(),
	})
}

func (e *Engine) recordDecision(ctx context.Context, incident store.Incident, resumeID, stage string, result decision.Result) error {
	record, _ := e.DB.ResumeForIncident(ctx, incident.Provider, incident.SessionID, incident.ID)
	attempt := record.Attempts
	if resumeID == "" {
		resumeID = record.ID
	}
	model := result.Model
	if model == "" {
		model = e.Config.GeminiModel
	}
	return e.DB.RecordDecision(ctx, store.Decision{
		ID: store.DecisionID(stage, incident.ID, attempt), IncidentID: incident.ID,
		ResumeID: resumeID, Stage: stage, Action: result.Action, Reason: result.Reason,
		Prompt: result.Prompt, Model: model, InputChars: result.InputChars,
		Redactions: result.Redactions, Fallback: result.Fallback,
		Simulated: incident.Simulated, CreatedAt: e.now(),
	})
}

// closeIncident stores a terminal state. Emails are sent by the caller.
func (e *Engine) closeIncident(ctx context.Context, incident store.Incident, state, reason string) error {
	if err := e.DB.UpdateIncident(ctx, incident.ID, incidentUpdate(incident, state, reason)); err != nil {
		return err
	}
	e.logf("incident %s -> %s: %s", incident.Provider, state, reason)
	return nil
}

func resumeKey(resumeID, phase string, attempt int) string {
	return fmt.Sprintf("resume:%s:%s:%d", resumeID, phase, attempt)
}

// incidentUpdate preserves the recorded session marker across state changes.
func incidentUpdate(incident store.Incident, state, reason string) store.IncidentUpdate {
	return store.IncidentUpdate{
		State: state, SessionID: incident.SessionID, SessionPath: incident.SessionPath,
		SessionCwd: incident.SessionCwd, CutOffAt: incident.CutOffAt,
		CutOffOffset: incident.CutOffOffset, Reason: reason,
	}
}

// interruptedResume reports a run left in flight by a crash.
func (e *Engine) interruptedResume(ctx context.Context, incident store.Incident) (store.Resume, bool) {
	record, err := e.DB.ResumeForIncident(ctx, incident.Provider, incident.SessionID, incident.ID)
	if err != nil {
		return store.Resume{}, false
	}
	return record, record.State == store.ResumeInterrupted
}

// resumeAfterCrash asks for the post-run decision without treating CarryOn's
// own interrupted run as someone else continuing the session.
func (e *Engine) resumeAfterCrash(ctx context.Context, incident store.Incident, found session.Session, record store.Resume) error {
	e.lastOutcome = resume.Outcome{ExitCode: -1, LastMessage: record.LastMessage}
	next, err := e.postDecision(ctx, incident, found)
	if err != nil {
		return e.blockOnDecision(ctx, incident, "post", err)
	}
	if next.Action == decision.ActionResumeAgain {
		return e.resumeLoop(ctx, incident, found, next)
	}
	return e.closeIncident(ctx, incident, store.IncidentFinished, next.Reason)
}

// attempts reports how many resumes this incident has already used.
func (e *Engine) attempts(ctx context.Context, incident store.Incident) int {
	record, err := e.DB.ResumeForIncident(ctx, incident.Provider, incident.SessionID, incident.ID)
	if err != nil {
		return 0
	}
	return record.Attempts
}
