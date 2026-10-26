package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Incident states.
const (
	IncidentDetected      = "detected"
	IncidentAwaitingReset = "awaiting_reset"
	IncidentDeciding      = "deciding"
	IncidentResuming      = "resuming"
	IncidentFinished      = "finished"
	IncidentSkipped       = "skipped"
	IncidentBlocked       = "blocked"
)

// Incident is one exhausted usage limit, recorded once.
type Incident struct {
	ID             string
	Provider       string
	AccountID      string
	WindowID       string
	WindowLabel    string
	UsedPercent    float64
	ResetAt        time.Time
	DetectedAt     time.Time
	State          string
	SessionID      string
	SessionPath    string
	SessionCwd     string
	CutOffAt       time.Time
	CutOffOffset   int64
	WindowDuration time.Duration
	Simulated      bool
	Reason         string
	UpdatedAt      time.Time
}

// IncidentKey is stable for one provider, account, window and reset time.
func IncidentKey(provider, accountID, windowID string, resetAt time.Time) string {
	payload, _ := json.Marshal([]any{provider, accountID, windowID, unix(resetAt)})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// CreateIncident inserts the incident, returning false when it already exists.
// The caller's ID is filled in when empty so later steps can refer to it.
func (d *DB) CreateIncident(ctx context.Context, incident *Incident) (bool, error) {
	now := time.Now().UTC()
	if incident.DetectedAt.IsZero() {
		incident.DetectedAt = now
	}
	if incident.State == "" {
		incident.State = IncidentDetected
	}
	if incident.ID == "" {
		incident.ID = IncidentKey(incident.Provider, incident.AccountID, incident.WindowID, incident.ResetAt)
	}
	result, err := d.db.ExecContext(ctx, `INSERT INTO incidents
 (id,provider,account_id,window_id,window_label,used_percent,reset_at,detected_at,state,
  session_id,session_path,session_cwd,cut_off_at,cut_off_offset,window_duration,simulated,reason,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(provider,account_id,window_id,reset_at) DO NOTHING`,
		incident.ID, incident.Provider, incident.AccountID, incident.WindowID, incident.WindowLabel,
		incident.UsedPercent, unix(incident.ResetAt), unix(incident.DetectedAt), incident.State,
		incident.SessionID, incident.SessionPath, incident.SessionCwd, unix(incident.CutOffAt),
		incident.CutOffOffset, int64(incident.WindowDuration/time.Second),
		boolInt(incident.Simulated), incident.Reason, unix(now))
	if err != nil {
		return false, fmt.Errorf("record incident: %w", err)
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

const incidentColumns = `id,provider,account_id,window_id,window_label,used_percent,reset_at,
 detected_at,state,session_id,session_path,session_cwd,cut_off_at,cut_off_offset,window_duration,simulated,reason,updated_at`

func scanIncident(row interface{ Scan(...any) error }) (Incident, error) {
	var incident Incident
	var reset, detected, cutOff, updated, duration int64
	var simulated int
	err := row.Scan(&incident.ID, &incident.Provider, &incident.AccountID, &incident.WindowID,
		&incident.WindowLabel, &incident.UsedPercent, &reset, &detected, &incident.State,
		&incident.SessionID, &incident.SessionPath, &incident.SessionCwd, &cutOff,
		&incident.CutOffOffset, &duration, &simulated, &incident.Reason, &updated)
	if err != nil {
		return Incident{}, err
	}
	incident.ResetAt = fromUnix(reset)
	incident.DetectedAt = fromUnix(detected)
	incident.CutOffAt = fromUnix(cutOff)
	incident.WindowDuration = time.Duration(duration) * time.Second
	incident.UpdatedAt = fromUnix(updated)
	incident.Simulated = simulated != 0
	return incident, nil
}

// Incident loads one incident by id.
func (d *DB) Incident(ctx context.Context, id string) (Incident, error) {
	row := d.db.QueryRowContext(ctx, "SELECT "+incidentColumns+" FROM incidents WHERE id=?", id)
	return scanIncident(row)
}

// Incidents lists incidents in a state, oldest first.
func (d *DB) Incidents(ctx context.Context, states ...string) ([]Incident, error) {
	query := "SELECT " + incidentColumns + " FROM incidents"
	args := make([]any, 0, len(states))
	if len(states) > 0 {
		query += " WHERE state IN (" + placeholders(len(states)) + ")"
		for _, state := range states {
			args = append(args, state)
		}
	}
	query += " ORDER BY detected_at ASC"
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var incidents []Incident
	for rows.Next() {
		incident, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		incidents = append(incidents, incident)
	}
	return incidents, rows.Err()
}

// IncidentUpdate carries the fields a state transition may change.
type IncidentUpdate struct {
	State        string
	SessionID    string
	SessionPath  string
	SessionCwd   string
	CutOffAt     time.Time
	CutOffOffset int64
	Reason       string
}

// UpdateIncident applies a state transition.
func (d *DB) UpdateIncident(ctx context.Context, id string, update IncidentUpdate) error {
	_, err := d.db.ExecContext(ctx, `UPDATE incidents SET state=?,session_id=?,session_path=?,
 session_cwd=?,cut_off_at=?,cut_off_offset=?,reason=?,updated_at=? WHERE id=?`,
		update.State, update.SessionID, update.SessionPath, update.SessionCwd,
		unix(update.CutOffAt), update.CutOffOffset, update.Reason, unix(time.Now()), id)
	if err != nil {
		return fmt.Errorf("update incident: %w", err)
	}
	return nil
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

// ResetInFlightIncidents returns incidents left mid-decision by a crash to the
// awaiting state, so they need a fresh decision before any resume.
func (d *DB) ResetInFlightIncidents(ctx context.Context) (int, error) {
	result, err := d.db.ExecContext(ctx, `UPDATE incidents SET state=?,updated_at=?
 WHERE state IN (?,?)`, IncidentAwaitingReset, unix(time.Now()), IncidentDeciding, IncidentResuming)
	if err != nil {
		return 0, fmt.Errorf("reset in-flight incidents: %w", err)
	}
	count, err := result.RowsAffected()
	return int(count), err
}
