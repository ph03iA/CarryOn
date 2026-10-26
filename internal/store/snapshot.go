package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Snapshot is the last usage reading for one provider.
type Snapshot struct {
	Provider   string
	Data       string
	ObservedAt time.Time
}

// SaveSnapshot stores the latest reading for a provider.
func (d *DB) SaveSnapshot(ctx context.Context, snapshot Snapshot) error {
	if !json.Valid([]byte(snapshot.Data)) {
		return fmt.Errorf("snapshot for %s is not valid JSON", snapshot.Provider)
	}
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = time.Now().UTC()
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO snapshots(provider,data,observed_at) VALUES(?,?,?)
 ON CONFLICT(provider) DO UPDATE SET data=excluded.data,observed_at=excluded.observed_at`,
		snapshot.Provider, snapshot.Data, unix(snapshot.ObservedAt))
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

// Snapshot loads the last reading for a provider.
func (d *DB) Snapshot(ctx context.Context, provider string) (Snapshot, error) {
	var snapshot Snapshot
	var observed int64
	err := d.db.QueryRowContext(ctx, "SELECT provider,data,observed_at FROM snapshots WHERE provider=?", provider).
		Scan(&snapshot.Provider, &snapshot.Data, &observed)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.ObservedAt = fromUnix(observed)
	return snapshot, nil
}
