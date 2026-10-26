package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Decision stages.
const (
	StagePre  = "pre"
	StagePost = "post"
)

// Decision is one Gemini call and its outcome, kept for audit.
type Decision struct {
	ID         string
	IncidentID string
	ResumeID   string
	Stage      string
	Action     string
	Reason     string
	Prompt     string
	Model      string
	InputChars int
	Redactions int
	Fallback   bool
	Simulated  bool
	CreatedAt  time.Time
}

// DecisionID derives a stable id from the stage, incident and attempt.
func DecisionID(stage, incidentID string, attempt int) string {
	payload, _ := json.Marshal([]any{stage, incidentID, attempt})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// RecordDecision stores a decision, replacing any earlier row with the same id.
func (d *DB) RecordDecision(ctx context.Context, decision Decision) error {
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = time.Now().UTC()
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO decisions
 (id,incident_id,resume_id,stage,action,reason,prompt,model,input_chars,redactions,
  fallback,simulated,created_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET action=excluded.action,reason=excluded.reason,prompt=excluded.prompt`,
		decision.ID, decision.IncidentID, decision.ResumeID, decision.Stage, decision.Action,
		decision.Reason, decision.Prompt, decision.Model, decision.InputChars,
		decision.Redactions, boolInt(decision.Fallback), boolInt(decision.Simulated),
		unix(decision.CreatedAt))
	if err != nil {
		return fmt.Errorf("record decision: %w", err)
	}
	return nil
}

const decisionColumns = `id,incident_id,resume_id,stage,action,reason,prompt,model,input_chars,
 redactions,fallback,simulated,created_at`

func scanDecision(row interface{ Scan(...any) error }) (Decision, error) {
	var decision Decision
	var fallback, simulated int
	var created int64
	err := row.Scan(&decision.ID, &decision.IncidentID, &decision.ResumeID, &decision.Stage,
		&decision.Action, &decision.Reason, &decision.Prompt, &decision.Model,
		&decision.InputChars, &decision.Redactions, &fallback, &simulated, &created)
	if err != nil {
		return Decision{}, err
	}
	decision.Fallback = fallback != 0
	decision.Simulated = simulated != 0
	decision.CreatedAt = fromUnix(created)
	return decision, nil
}

// Decisions lists recorded decisions newest first.
func (d *DB) Decisions(ctx context.Context, limit int) ([]Decision, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT "+decisionColumns+
		" FROM decisions ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decisions []Decision
	for rows.Next() {
		decision, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, rows.Err()
}

// DecisionsForIncident lists decisions for one incident, oldest first.
func (d *DB) DecisionsForIncident(ctx context.Context, incidentID string) ([]Decision, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT "+decisionColumns+
		" FROM decisions WHERE incident_id=? ORDER BY created_at ASC", incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decisions []Decision
	for rows.Next() {
		decision, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, rows.Err()
}
