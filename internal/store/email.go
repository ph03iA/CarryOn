package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Email states.
const (
	EmailPending = "pending"
	EmailSending = "sending"
	EmailSent    = "sent"
	EmailRetry   = "retry"
	EmailFailed  = "failed"
)

// Email is one durable outbox entry. EventKey makes each event unique.
type Email struct {
	ID           string
	EventKey     string
	Kind         string
	Subject      string
	Body         string
	State        string
	Attempts     int
	FirstAttempt time.Time
	NextAttempt  time.Time
	LeaseUntil   time.Time
	LastError    string
	Simulated    bool
	UpdatedAt    time.Time
}

// EmailID derives a stable row id from the event key.
func EmailID(eventKey string) string {
	sum := sha256.Sum256([]byte(eventKey))
	return hex.EncodeToString(sum[:])
}

// EnqueueEmail stores one email event. Repeating an event is a no-op.
func (d *DB) EnqueueEmail(ctx context.Context, email Email) (bool, error) {
	now := time.Now().UTC()
	if email.ID == "" {
		email.ID = EmailID(email.EventKey)
	}
	if email.State == "" {
		email.State = EmailPending
	}
	if email.NextAttempt.IsZero() {
		email.NextAttempt = now
	}
	result, err := d.db.ExecContext(ctx, `INSERT INTO emails
 (id,event_key,kind,subject,body,state,attempts,first_attempt,next_attempt,lease_until,
  last_error,simulated,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(event_key) DO NOTHING`,
		email.ID, email.EventKey, email.Kind, email.Subject, email.Body, email.State,
		email.Attempts, unix(email.FirstAttempt), unix(email.NextAttempt), unix(email.LeaseUntil),
		email.LastError, boolInt(email.Simulated), unix(now))
	if err != nil {
		return false, fmt.Errorf("enqueue email: %w", err)
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

const emailColumns = `id,event_key,kind,subject,body,state,attempts,first_attempt,
 next_attempt,lease_until,last_error,simulated,updated_at`

func scanEmail(row interface{ Scan(...any) error }) (Email, error) {
	var email Email
	var first, next, lease, updated int64
	var simulated int
	err := row.Scan(&email.ID, &email.EventKey, &email.Kind, &email.Subject, &email.Body,
		&email.State, &email.Attempts, &first, &next, &lease, &email.LastError,
		&simulated, &updated)
	if err != nil {
		return Email{}, err
	}
	email.FirstAttempt = fromUnix(first)
	email.NextAttempt = fromUnix(next)
	email.LeaseUntil = fromUnix(lease)
	email.UpdatedAt = fromUnix(updated)
	email.Simulated = simulated != 0
	return email, nil
}

// ErrNoEmail means the outbox has nothing due.
var ErrNoEmail = errors.New("no email due")

// ClaimEmail leases the next due email for sending.
func (d *DB) ClaimEmail(ctx context.Context, now time.Time, lease time.Duration) (Email, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return Email{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, "SELECT "+emailColumns+` FROM emails
 WHERE state IN (?,?) AND next_attempt<=? AND lease_until<=?
 ORDER BY next_attempt ASC LIMIT 1`, EmailPending, EmailRetry, unix(now), unix(now))
	email, err := scanEmail(row)
	if err != nil {
		if isNoRows(err) {
			return Email{}, ErrNoEmail
		}
		return Email{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE emails
 SET state=?,attempts=attempts+1,first_attempt=CASE WHEN first_attempt=0 THEN ? ELSE first_attempt END,
 lease_until=?,updated_at=? WHERE id=?`,
		EmailSending, unix(now), unix(now.Add(lease)), unix(now), email.ID)
	if err != nil {
		return Email{}, err
	}
	email.State = EmailSending
	email.Attempts++
	if email.FirstAttempt.IsZero() {
		email.FirstAttempt = now
	}
	email.LeaseUntil = now.Add(lease)
	return email, tx.Commit()
}

// FinishEmail records a delivery outcome.
func (d *DB) FinishEmail(ctx context.Context, id, state, lastError string, next time.Time) error {
	_, err := d.db.ExecContext(ctx, `UPDATE emails
 SET state=?,last_error=?,next_attempt=?,lease_until=0,updated_at=? WHERE id=? AND state=?`,
		state, lastError, unix(next), unix(time.Now()), id, EmailSending)
	if err != nil {
		return fmt.Errorf("finish email: %w", err)
	}
	return nil
}

// Emails lists outbox rows newest first.
func (d *DB) Emails(ctx context.Context, limit int) ([]Email, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT "+emailColumns+
		" FROM emails ORDER BY updated_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var emails []Email
	for rows.Next() {
		email, err := scanEmail(rows)
		if err != nil {
			return nil, err
		}
		emails = append(emails, email)
	}
	return emails, rows.Err()
}

// EmailByEventKey loads an email by its event key.
func (d *DB) EmailByEventKey(ctx context.Context, eventKey string) (Email, error) {
	row := d.db.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails WHERE event_key=?", eventKey)
	return scanEmail(row)
}

// ReleaseStaleLeases returns emails stuck in sending after a crash to the queue.
func (d *DB) ReleaseStaleLeases(ctx context.Context, now time.Time) (int, error) {
	result, err := d.db.ExecContext(ctx, `UPDATE emails
 SET state=?,lease_until=0,updated_at=? WHERE state=? AND lease_until<=?`,
		EmailRetry, unix(now), EmailSending, unix(now))
	if err != nil {
		return 0, fmt.Errorf("release stale leases: %w", err)
	}
	count, err := result.RowsAffected()
	return int(count), err
}
