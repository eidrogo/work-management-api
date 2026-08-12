package seed

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
)

const (
	ManagerID        = "11111111-1111-4111-8111-111111111111"
	WorkerAliceID    = "22222222-2222-4222-8222-222222222222"
	WorkerBobID      = "33333333-3333-4333-8333-333333333333"
	ManagerToken     = "dev-manager-token"
	WorkerAliceToken = "dev-worker-alice-token"
	WorkerBobToken   = "dev-worker-bob-token"
)

type Summary struct{ Users, WorkItems int }

func Apply(ctx context.Context, db *sql.DB, current clock.Clock) (Summary, error) {
	if db == nil || current == nil {
		return Summary{}, fmt.Errorf("database and clock are required")
	}
	now := current.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Summary{}, err
	}
	defer func() { _ = tx.Rollback() }()
	users := []struct{ id, name, email, role, token string }{
		{ManagerID, "Maya Manager", "maya.manager@example.test", "MANAGER", ManagerToken},
		{WorkerAliceID, "Alice Worker", "alice.worker@example.test", "WORKER", WorkerAliceToken},
		{WorkerBobID, "Bob Worker", "bob.worker@example.test", "WORKER", WorkerBobToken},
	}
	for _, user := range users {
		if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,display_name,email,role,created_at) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(id) DO UPDATE SET display_name=EXCLUDED.display_name,email=EXCLUDED.email,role=EXCLUDED.role`, user.id, user.name, user.email, user.role, now); err != nil {
			return Summary{}, fmt.Errorf("seed user: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO auth_tokens(token_hash,user_id,created_at) VALUES($1,$2,$3)
ON CONFLICT(token_hash) DO UPDATE SET user_id=EXCLUDED.user_id`, auth.HashToken(user.token), user.id, now); err != nil {
			return Summary{}, fmt.Errorf("seed token: %w", err)
		}
	}
	samples := []struct{ id, event, title, description string }{
		{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "aaaaaaaa-0000-4000-8000-000000000001", "Review supplier exception", "Investigate and document the flagged supplier exception."},
		{"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "bbbbbbbb-0000-4000-8000-000000000001", "Prepare weekly operations summary", "Collect the current queue metrics for the weekly review."},
	}
	for _, sample := range samples {
		if _, err = tx.ExecContext(ctx, `WITH inserted AS (
INSERT INTO work_items(id,title,description,status,creator_id,assignee_id,version,created_at,updated_at)
VALUES($1,$2,$3,'UNASSIGNED',$4,NULL,1,$5,$5) ON CONFLICT(id) DO NOTHING RETURNING id)
INSERT INTO work_item_events(public_id,work_item_id,actor_id,event_type,from_status,to_status,reason,occurred_at)
SELECT $6,id,$4,'WORK_ITEM_CREATED',NULL,'UNASSIGNED','seed data',$5 FROM inserted`, sample.id, sample.title, sample.description, ManagerID, now, sample.event); err != nil {
			return Summary{}, fmt.Errorf("seed work item: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return Summary{}, err
	}
	return Summary{Users: len(users), WorkItems: len(samples)}, nil
}
