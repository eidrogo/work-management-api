package seed

import (
	"context"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/testsupport/postgrestest"
)

func TestApplyIsIdempotent(t *testing.T) {
	db := postgrestest.Open(t)
	current := clock.Fixed{Time: time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)}
	if _, err := Apply(context.Background(), db, current); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), db, current); err != nil {
		t.Fatal(err)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM users`); got != 3 {
		t.Fatalf("users=%d", got)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_items`); got != 2 {
		t.Fatalf("work items=%d", got)
	}
}
