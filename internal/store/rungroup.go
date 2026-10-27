package store

import (
	"context"
	"fmt"
	"time"
)

// RunGroup is the process group of an agent run that is still in flight. A row
// left behind at startup means CarryOn died while that agent was running.
type RunGroup struct {
	ResumeID  string
	PGID      int
	StartedAt time.Time
}

// SaveRunGroup records the process group of a run that just started.
func (d *DB) SaveRunGroup(ctx context.Context, resumeID string, pgid int, startedAt time.Time) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO run_groups(resume_id,pgid,started_at) VALUES(?,?,?)
 ON CONFLICT(resume_id) DO UPDATE SET pgid=excluded.pgid,started_at=excluded.started_at`,
		resumeID, pgid, unix(startedAt))
	if err != nil {
		return fmt.Errorf("save run group: %w", err)
	}
	return nil
}

// DeleteRunGroup forgets a run's process group once the run has ended.
func (d *DB) DeleteRunGroup(ctx context.Context, resumeID string) error {
	if _, err := d.db.ExecContext(ctx, "DELETE FROM run_groups WHERE resume_id=?", resumeID); err != nil {
		return fmt.Errorf("delete run group: %w", err)
	}
	return nil
}

// RunGroups lists the process groups of runs that never recorded their end.
func (d *DB) RunGroups(ctx context.Context) ([]RunGroup, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT resume_id,pgid,started_at FROM run_groups")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []RunGroup
	for rows.Next() {
		var group RunGroup
		var started int64
		if err := rows.Scan(&group.ResumeID, &group.PGID, &started); err != nil {
			return nil, err
		}
		group.StartedAt = fromUnix(started)
		groups = append(groups, group)
	}
	return groups, rows.Err()
}
