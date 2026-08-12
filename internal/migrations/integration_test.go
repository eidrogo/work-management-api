package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/testsupport/postgrestest"
)

func TestFreshMigrationCreatesConstraints(t *testing.T) {
	db := postgrestest.Open(t)
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM schema_migrations`); got != 1 {
		t.Fatalf("schema migrations=%d", got)
	}
	now := time.Now().UTC()
	postgrestest.MustExec(t, db, `INSERT INTO users(id,display_name,email,role,created_at) VALUES
('11111111-1111-4111-8111-111111111111','Manager','manager@example.test','MANAGER',$1),
('22222222-2222-4222-8222-222222222222','Worker','worker@example.test','WORKER',$1)`, now)
	_, err := db.ExecContext(context.Background(), `INSERT INTO work_items(id,title,description,status,creator_id,assignee_id,version,created_at,updated_at)
VALUES('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','Invalid','','UNASSIGNED','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222',1,$1,$1)`, now)
	if err == nil {
		t.Fatal("status/assignee constraint accepted impossible state")
	}
}
