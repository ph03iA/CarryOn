package store

import (
	"context"
	"testing"
	"time"
)

func TestRunGroupsRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	started := time.Date(2026, 10, 27, 9, 30, 0, 0, time.UTC)
	if err := db.SaveRunGroup(ctx, "resume-1", 4242, started); err != nil {
		t.Fatal(err)
	}
	groups, err := db.RunGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].ResumeID != "resume-1" || groups[0].PGID != 4242 || !groups[0].StartedAt.Equal(started) {
		t.Fatalf("unexpected run groups: %+v", groups)
	}
	if err := db.DeleteRunGroup(ctx, "resume-1"); err != nil {
		t.Fatal(err)
	}
	if groups, _ := db.RunGroups(ctx); len(groups) != 0 {
		t.Fatalf("run group not deleted: %+v", groups)
	}
}
