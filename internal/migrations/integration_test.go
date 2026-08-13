package migrations_test

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/migrations"
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

func TestInitialDownMigrationDropsTask2Objects(t *testing.T) {
	db := postgrestest.Open(t)
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migrations path")
	}
	directory := filepath.Clean(filepath.Join(filepath.Dir(file), "../..", "migrations"))
	runner := migrations.Runner{DB: db, Dir: directory}
	changed, err := runner.Down(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0].Version != 1 {
		t.Fatalf("changed=%#v", changed)
	}
	for _, relation := range []string{"notification_replay_events", "notification_attempts", "notification_deliveries", "idempotency_keys", "work_items"} {
		if got := postgrestest.Count(t, db, `SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname=$1`, relation); got != 0 {
			t.Fatalf("relation %s remains", relation)
		}
	}
}

func TestFreshMigrationHasTask2Hardening(t *testing.T) {
	db := postgrestest.Open(t)

	columns := []struct {
		table, column, dataType string
		nullable                bool
		maxLength               int
	}{
		{table: "idempotency_keys", column: "response_snapshot", dataType: "jsonb", nullable: false},
		{table: "notification_deliveries", column: "delivery_cycle", dataType: "integer", nullable: false},
		{table: "notification_deliveries", column: "last_error_code", dataType: "character varying", nullable: true, maxLength: 64},
		{table: "notification_deliveries", column: "last_error", dataType: "character varying", nullable: true, maxLength: 500},
		{table: "notification_deliveries", column: "lease_token", dataType: "uuid", nullable: true},
		{table: "notification_attempts", column: "error_code", dataType: "character varying", nullable: true, maxLength: 64},
		{table: "notification_attempts", column: "error_message", dataType: "character varying", nullable: true, maxLength: 500},
	}
	for _, want := range columns {
		var dataType, nullable string
		var maxLength *int
		err := db.QueryRowContext(context.Background(), `SELECT data_type,is_nullable,character_maximum_length
FROM information_schema.columns
WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, want.table, want.column).
			Scan(&dataType, &nullable, &maxLength)
		if err != nil {
			t.Fatalf("column %s.%s: %v", want.table, want.column, err)
		}
		if dataType != want.dataType || (nullable == "YES") != want.nullable {
			t.Fatalf("column %s.%s type=%q nullable=%q", want.table, want.column, dataType, nullable)
		}
		if want.maxLength > 0 && (maxLength == nil || *maxLength != want.maxLength) {
			t.Fatalf("column %s.%s max length=%v", want.table, want.column, maxLength)
		}
	}

	for _, constraint := range []struct {
		name, contains string
	}{
		{name: "notification_delivery_lease_ck", contains: "lease_token"},
		{name: "notification_delivery_schedule_ck", contains: "'PENDING'"},
		{name: "notification_delivery_error_ck", contains: "last_error_code"},
		{name: "notification_attempt_error_ck", contains: "error_code"},
	} {
		var definition string
		if err := db.QueryRowContext(context.Background(), `SELECT pg_get_constraintdef(oid)
FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname=$1`, constraint.name).Scan(&definition); err != nil {
			t.Fatalf("constraint %s: %v", constraint.name, err)
		}
		if !strings.Contains(definition, constraint.contains) {
			t.Fatalf("constraint %s=%q", constraint.name, definition)
		}
	}
	for _, index := range []string{
		"notification_deliveries_lease_token_uq",
		"work_items_assignee_page_idx",
		"notification_deliveries_page_idx",
		"notification_replay_events_notification_idx",
	} {
		if got := postgrestest.Count(t, db, `SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname=$1`, index); got != 1 {
			t.Fatalf("index %s count=%d", index, got)
		}
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM pg_trigger WHERE tgrelid='notification_replay_events'::regclass AND tgname='notification_replay_events_append_only' AND NOT tgisinternal`); got != 1 {
		t.Fatalf("replay append-only triggers=%d", got)
	}

	now := time.Now().UTC()
	postgrestest.MustExec(t, db, `INSERT INTO users(id,display_name,email,role,created_at) VALUES
('11111111-1111-4111-8111-111111111111','Manager','manager-task2@example.test','MANAGER',$1),
('22222222-2222-4222-8222-222222222222','Worker','worker-task2@example.test','WORKER',$1)`, now)
	postgrestest.MustExec(t, db, `INSERT INTO work_items(id,title,description,status,creator_id,assignee_id,version,created_at,updated_at)
VALUES('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','Task 2','','ASSIGNED','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222',2,$1,$1)`, now)
	var eventID int64
	if err := db.QueryRowContext(context.Background(), `INSERT INTO work_item_events(public_id,work_item_id,actor_id,event_type,from_status,to_status,new_assignee_id,occurred_at)
VALUES('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','11111111-1111-4111-8111-111111111111','WORK_ITEM_ASSIGNED','UNASSIGNED','ASSIGNED','22222222-2222-4222-8222-222222222222',$1) RETURNING id`, now).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	postgrestest.MustExec(t, db, `INSERT INTO notification_deliveries(
id,work_item_id,assignment_event_id,recipient_email,recipient_name,event_type,payload,status,attempt_count,delivery_cycle,cycle_attempt_count,next_attempt_at,last_error_code,last_error,message_id,created_at,updated_at,deduplication_key)
VALUES('cccccccc-cccc-4ccc-8ccc-cccccccccccc','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',$1,'worker-task2@example.test','Worker','WORK_ITEM_ASSIGNED_EMAIL','{}','FAILED',1,2,1,NULL,'DELIVERY_FAILED','The notification could not be delivered.','<task2@example.test>',$2,$2,'task2-migration')`, eventID, now)

	if _, err := db.ExecContext(context.Background(), `UPDATE notification_deliveries SET last_error_code='PROVIDER_SECRET' WHERE id='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`); err == nil {
		t.Fatal("invalid delivery failure code was accepted")
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE notification_deliveries SET last_error_code=NULL WHERE id='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`); err == nil {
		t.Fatal("unpaired delivery failure message was accepted")
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE notification_deliveries SET delivery_cycle=11 WHERE id='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`); err == nil {
		t.Fatal("delivery cycle 11 was accepted")
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE notification_deliveries SET status='PROCESSING',locked_by='worker',locked_until=clock_timestamp() WHERE id='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`); err == nil {
		t.Fatal("processing delivery without lease token was accepted")
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE notification_deliveries SET status='PENDING' WHERE id='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`); err == nil {
		t.Fatal("pending delivery without next_attempt_at was accepted")
	}
	if _, err := db.ExecContext(context.Background(), `INSERT INTO notification_attempts(notification_id,attempt_number,worker_id,outcome,error_code,error_message,started_at,finished_at)
VALUES('cccccccc-cccc-4ccc-8ccc-cccccccccccc',1,'worker','FAILED','PROVIDER_SECRET','unsafe',$1,$1)`, now); err == nil {
		t.Fatal("invalid attempt failure code was accepted")
	}
	postgrestest.MustExec(t, db, `INSERT INTO notification_replay_events(notification_id,delivery_cycle,actor_id,request_id,channel,reason,occurred_at)
VALUES('cccccccc-cccc-4ccc-8ccc-cccccccccccc',2,'operator','request-task2','CLI','Investigate delivery',$1)`, now)
	if _, err := db.ExecContext(context.Background(), `UPDATE notification_replay_events SET reason='mutated' WHERE notification_id='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`); err == nil {
		t.Fatal("replay event update was accepted")
	}
	if _, err := db.ExecContext(context.Background(), `DELETE FROM notification_replay_events WHERE notification_id='cccccccc-cccc-4ccc-8ccc-cccccccccccc'`); err == nil {
		t.Fatal("replay event delete was accepted")
	}
}
