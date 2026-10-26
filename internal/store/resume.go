package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Resume states.
const (
	ResumePending     = "pending"
	ResumeClaimed     = "claimed"
	ResumeRunning     = "running"
	ResumeFinished    = "finished"
	ResumeInterrupted = "interrupted"
	ResumeFailed      = "failed"
)

// Resume is one attempt to continue a cut-off session.
type Resume struct {
	ID           string
	IncidentID   string
	Provider     string
	SessionID    string
	Cwd          string
	Attempts     int
	State        string
	Prompt       string
	DecisionID   string
	ExitCode     int
	StartedAt    time.Time
	FinishedAt   time.Time
	LastMessage  string
	FilesChanged string
	Simulated    bool
	UpdatedAt    time.Time
}

// ResumeID is stable for one provider, session and incident.
func ResumeID(provider, sessionID, incidentID string) string {
	payload, _ := json.Marshal([]any{provider, sessionID, incidentID})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// CreateResume records a session as resumable, returning false when it exists.
// The caller's ID is filled in when empty so later steps can refer to it.
func (d *DB) CreateResume(ctx context.Context, resume *Resume) (bool, error) {
	now := time.Now().UTC()
	if resume.ID == "" {
		resume.ID = ResumeID(resume.Provider, resume.SessionID, resume.IncidentID)
	}
	if resume.State == "" {
		resume.State = ResumePending
	}
	result, err := d.db.ExecContext(ctx, `INSERT INTO resumes
 (id,incident_id,provider,session_id,cwd,attempts,state,prompt,decision_id,exit_code,
  started_at,finished_at,last_message,files_changed,simulated,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(provider,session_id,incident_id) DO NOTHING`,
		resume.ID, resume.IncidentID, resume.Provider, resume.SessionID, resume.Cwd,
		resume.Attempts, resume.State, resume.Prompt, resume.DecisionID, resume.ExitCode,
		unix(resume.StartedAt), unix(resume.FinishedAt), resume.LastMessage,
		resume.FilesChanged, boolInt(resume.Simulated), unix(now))
	if err != nil {
		return false, fmt.Errorf("record resume: %w", err)
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

const resumeColumns = `id,incident_id,provider,session_id,cwd,attempts,state,prompt,decision_id,
 exit_code,started_at,finished_at,last_message,files_changed,simulated,updated_at`

func scanResume(row interface{ Scan(...any) error }) (Resume, error) {
	var resume Resume
	var started, finished, updated int64
	var simulated int
	err := row.Scan(&resume.ID, &resume.IncidentID, &resume.Provider, &resume.SessionID,
		&resume.Cwd, &resume.Attempts, &resume.State, &resume.Prompt, &resume.DecisionID,
		&resume.ExitCode, &started, &finished, &resume.LastMessage, &resume.FilesChanged,
		&simulated, &updated)
	if err != nil {
		return Resume{}, err
	}
	resume.StartedAt = fromUnix(started)
	resume.FinishedAt = fromUnix(finished)
	resume.UpdatedAt = fromUnix(updated)
	resume.Simulated = simulated != 0
	return resume, nil
}

// Resume loads one resume row by id.
func (d *DB) Resume(ctx context.Context, id string) (Resume, error) {
	row := d.db.QueryRowContext(ctx, "SELECT "+resumeColumns+" FROM resumes WHERE id=?", id)
	return scanResume(row)
}

// ResumeForIncident loads the resume row for one session and incident.
func (d *DB) ResumeForIncident(ctx context.Context, provider, sessionID, incidentID string) (Resume, error) {
	row := d.db.QueryRowContext(ctx, "SELECT "+resumeColumns+
		" FROM resumes WHERE provider=? AND session_id=? AND incident_id=?",
		provider, sessionID, incidentID)
	return scanResume(row)
}

// ClaimResume moves a resume to claimed, consuming one attempt. It returns
// false when the attempt budget is spent or the row is mid-run. Finished and
// interrupted runs may be claimed again, always after a fresh decision.
func (d *DB) ClaimResume(ctx context.Context, id string, maxAttempts int, prompt string) (bool, error) {
	result, err := d.db.ExecContext(ctx, `UPDATE resumes
 SET state=?,attempts=attempts+1,prompt=?,updated_at=?
 WHERE id=? AND state IN (?,?,?,?) AND attempts < ?`,
		ResumeClaimed, prompt, unix(time.Now()), id,
		ResumePending, ResumeInterrupted, ResumeFinished, ResumeFailed, maxAttempts)
	if err != nil {
		return false, fmt.Errorf("claim resume: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// StartResume marks a claimed resume as running.
func (d *DB) StartResume(ctx context.Context, id string, decisionID string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE resumes
 SET state=?,started_at=?,decision_id=?,updated_at=? WHERE id=? AND state=?`,
		ResumeRunning, unix(time.Now()), decisionID, unix(time.Now()), id, ResumeClaimed)
	if err != nil {
		return fmt.Errorf("start resume: %w", err)
	}
	return nil
}

// FinishResume stores the outcome of a completed run.
func (d *DB) FinishResume(ctx context.Context, id string, exitCode int, lastMessage, files string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE resumes
 SET state=?,exit_code=?,finished_at=?,last_message=?,files_changed=?,updated_at=?
 WHERE id=? AND state=?`,
		ResumeFinished, exitCode, unix(time.Now()), lastMessage, files, unix(time.Now()),
		id, ResumeRunning)
	if err != nil {
		return fmt.Errorf("finish resume: %w", err)
	}
	return nil
}

// FailResume records a resume that could not run.
func (d *DB) FailResume(ctx context.Context, id string, reason string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE resumes
 SET state=?,finished_at=?,last_message=?,updated_at=? WHERE id=? AND state IN (?,?)`,
		ResumeFailed, unix(time.Now()), reason, unix(time.Now()), id, ResumeClaimed, ResumeRunning)
	if err != nil {
		return fmt.Errorf("fail resume: %w", err)
	}
	return nil
}

// ReconcileInterrupted marks runs left in flight by a crash. They keep their
// consumed attempt and are never restarted without a new decision.
func (d *DB) ReconcileInterrupted(ctx context.Context) (int, error) {
	result, err := d.db.ExecContext(ctx, `UPDATE resumes
 SET state=?,finished_at=?,last_message=?,updated_at=? WHERE state=?`,
		ResumeInterrupted, unix(time.Now()), "CarryOn stopped while this run was in flight",
		unix(time.Now()), ResumeRunning)
	if err != nil {
		return 0, fmt.Errorf("reconcile interrupted runs: %w", err)
	}
	count, err := result.RowsAffected()
	return int(count), err
}

// Resumes lists resumes newest first.
func (d *DB) Resumes(ctx context.Context, limit int) ([]Resume, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT "+resumeColumns+
		" FROM resumes ORDER BY updated_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var resumes []Resume
	for rows.Next() {
		resume, err := scanResume(rows)
		if err != nil {
			return nil, err
		}
		resumes = append(resumes, resume)
	}
	return resumes, rows.Err()
}
