package workitems

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/platform/id"
	"github.com/eidrogo/work-management-api/internal/seed"
	"github.com/eidrogo/work-management-api/internal/testsupport/postgrestest"
)

func TestManagerToWorkerFlowIsAtomicAndTraceable(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	if _, err := seed.Apply(context.Background(), db, clock.Fixed{Time: now}); err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya"}
	alice := auth.Principal{ID: seed.WorkerAliceID, Role: auth.RoleWorker, DisplayName: "Alice"}
	created, err := service.Create(context.Background(), manager, CreateInput{Title: "Inspect warehouse", IdempotencyKey: "flow-create"})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: alice.ID, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: alice.ID})
	if err != nil || !replay.Idempotent {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM notification_deliveries WHERE work_item_id=$1`, created.Item.ID); got != 1 {
		t.Fatalf("notifications=%d", got)
	}
	started, err := service.Start(context.Background(), alice, created.Item.ID, TransitionInput{ExpectedVersion: assigned.Item.Version})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.Complete(context.Background(), alice, created.Item.ID, TransitionInput{ExpectedVersion: started.Item.Version})
	if err != nil || completed.Item.Status != StatusCompleted {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	history, err := service.History(context.Background(), alice, created.Item.ID, HistoryInput{})
	if err != nil || len(history.Items) != 4 {
		t.Fatalf("history=%#v err=%v", history, err)
	}
}

func TestNotificationInsertFailureRollsBackAssignment(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya"}
	created, _ := service.Create(context.Background(), manager, CreateInput{Title: "Atomic assignment", IdempotencyKey: "atomic-create"})
	postgrestest.MustExec(t, db, `CREATE FUNCTION reject_test_notification() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'test rejection'; END; $$ LANGUAGE plpgsql`)
	postgrestest.MustExec(t, db, `CREATE TRIGGER reject_test_notification BEFORE INSERT ON notification_deliveries FOR EACH ROW EXECUTE FUNCTION reject_test_notification()`)
	_, err := service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: seed.WorkerAliceID, ExpectedVersion: 1})
	if err == nil {
		t.Fatal("assignment unexpectedly succeeded")
	}
	item, err := service.Get(context.Background(), manager, created.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != StatusUnassigned || item.Version != 1 {
		t.Fatalf("item=%#v", item)
	}
}

func TestConcurrentAssignmentHasOneWinner(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya"}
	created, _ := service.Create(context.Background(), manager, CreateInput{Title: "Concurrent", IdempotencyKey: "concurrent-create"})
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, workerID := range []string{seed.WorkerAliceID, seed.WorkerBobID} {
		workerID := workerID
		go func() {
			<-start
			_, err := service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: workerID, Reason: "concurrency", ExpectedVersion: 1})
			results <- err
		}()
	}
	close(start)
	var success, conflict int
	for index := 0; index < 2; index++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrConcurrentModification) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}
