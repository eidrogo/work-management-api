package workitems

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_item_events WHERE work_item_id=$1`, created.Item.ID); got != 1 {
		t.Fatalf("events after rollback=%d", got)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM notification_deliveries WHERE work_item_id=$1`, created.Item.ID); got != 0 {
		t.Fatalf("deliveries after rollback=%d", got)
	}
}

func TestCreateIdempotencyReturnsImmutableSnapshot(t *testing.T) {
	db := postgrestest.Open(t)
	now := databaseNowForWorkItems(t, db)
	if _, err := seed.Apply(context.Background(), db, clock.Fixed{Time: now}); err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya Manager"}
	alice := auth.Principal{ID: seed.WorkerAliceID, Role: auth.RoleWorker, DisplayName: "Alice Worker"}
	input := CreateInput{Title: "Immutable create", Description: "Original response", IdempotencyKey: "immutable-create"}
	created, err := service.Create(context.Background(), manager, input)
	if err != nil {
		t.Fatal(err)
	}
	original := created.Item
	assigned, err := service.Assign(context.Background(), manager, original.ID, AssignmentInput{AssigneeID: alice.ID, ExpectedVersion: original.Version})
	if err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(context.Background(), alice, original.ID, TransitionInput{ExpectedVersion: assigned.Item.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Complete(context.Background(), alice, original.ID, TransitionInput{ExpectedVersion: started.Item.Version}); err != nil {
		t.Fatal(err)
	}
	postgrestest.MustExec(t, db, `UPDATE users SET display_name='Renamed Manager' WHERE id=$1`, seed.ManagerID)
	replayed, err := service.Create(context.Background(), manager, input)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Idempotent || !reflect.DeepEqual(replayed.Item, original) {
		t.Fatalf("original=%#v replayed=%#v", original, replayed)
	}
	if replayed.Item.Status != StatusUnassigned || replayed.Item.Version != 1 || replayed.Item.CreatorName != "Maya Manager" {
		t.Fatalf("replayed=%#v", replayed.Item)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_items WHERE title='Immutable create'`); got != 1 {
		t.Fatalf("work items=%d", got)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_item_events WHERE work_item_id=$1 AND event_type='WORK_ITEM_CREATED'`, original.ID); got != 1 {
		t.Fatalf("creation events=%d", got)
	}
	if _, err = service.Create(context.Background(), manager, CreateInput{Title: "Different", IdempotencyKey: input.IdempotencyKey}); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("different payload error=%v", err)
	}
}

func TestConcurrentCreateIdempotencyKeepsSingleCandidateAndSnapshot(t *testing.T) {
	db := postgrestest.Open(t)
	now := databaseNowForWorkItems(t, db)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager}
	input := CreateInput{Title: "Concurrent create snapshot", Description: "same request", IdempotencyKey: "concurrent-create-snapshot"}
	start := make(chan struct{})
	type result struct {
		mutation MutationResult
		err      error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			mutation, err := service.Create(context.Background(), manager, input)
			results <- result{mutation: mutation, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("errors=%v %v", first.err, second.err)
	}
	if first.mutation.Idempotent == second.mutation.Idempotent || !reflect.DeepEqual(first.mutation.Item, second.mutation.Item) {
		t.Fatalf("first=%#v second=%#v", first.mutation, second.mutation)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_items WHERE title=$1`, input.Title); got != 1 {
		t.Fatalf("work items=%d", got)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_item_events WHERE work_item_id=$1 AND event_type='WORK_ITEM_CREATED'`, first.mutation.Item.ID); got != 1 {
		t.Fatalf("creation events=%d", got)
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

func TestServiceAuthorizationAndLifecycleMatrix(t *testing.T) {
	db := postgrestest.Open(t)
	now := databaseNowForWorkItems(t, db)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya"}
	alice := auth.Principal{ID: seed.WorkerAliceID, Role: auth.RoleWorker, DisplayName: "Alice"}
	bob := auth.Principal{ID: seed.WorkerBobID, Role: auth.RoleWorker, DisplayName: "Bob"}

	if _, err := service.Create(context.Background(), alice, CreateInput{Title: "Denied", IdempotencyKey: "worker-create"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("worker create error=%v", err)
	}
	if _, err := service.ListManager(context.Background(), alice, ListInput{}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("worker manager-list error=%v", err)
	}
	if _, err := service.ListOwn(context.Background(), manager, ListInput{}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("manager own-list error=%v", err)
	}
	if _, err := service.ListManager(context.Background(), manager, ListInput{}); err != nil {
		t.Fatalf("manager list error=%v", err)
	}
	if _, err := service.ListOwn(context.Background(), alice, ListInput{}); err != nil {
		t.Fatalf("worker own-list error=%v", err)
	}

	created, err := service.Create(context.Background(), manager, CreateInput{Title: "Authorization lifecycle", IdempotencyKey: "auth-lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Assign(context.Background(), alice, created.Item.ID, AssignmentInput{AssigneeID: alice.ID, ExpectedVersion: 1}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("worker assignment error=%v", err)
	}
	if _, err = service.Start(context.Background(), manager, created.Item.ID, TransitionInput{ExpectedVersion: 1}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("manager start error=%v", err)
	}
	assigned, err := service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: alice.ID, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Get(context.Background(), bob, created.Item.ID); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("other worker get error=%v", err)
	}
	if _, err = service.History(context.Background(), bob, created.Item.ID, HistoryInput{}); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("other worker history error=%v", err)
	}
	if _, err = service.Start(context.Background(), bob, created.Item.ID, TransitionInput{ExpectedVersion: assigned.Item.Version}); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("other worker start error=%v", err)
	}
	if _, err = service.Complete(context.Background(), alice, created.Item.ID, TransitionInput{ExpectedVersion: assigned.Item.Version}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("complete before start error=%v", err)
	}
	if _, err = service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: bob.ID, ExpectedVersion: assigned.Item.Version}); !errors.Is(err, ErrReassignmentReasonRequired) {
		t.Fatalf("reasonless reassignment error=%v", err)
	}

	invalid, err := service.Create(context.Background(), manager, CreateInput{Title: "Invalid assignee", IdempotencyKey: "invalid-assignee"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Assign(context.Background(), manager, invalid.Item.ID, AssignmentInput{AssigneeID: manager.ID, ExpectedVersion: 1}); !errors.Is(err, ErrAssigneeMustBeWorker) {
		t.Fatalf("manager assignee error=%v", err)
	}

	started, err := service.Start(context.Background(), alice, created.Item.ID, TransitionInput{ExpectedVersion: assigned.Item.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Complete(context.Background(), manager, created.Item.ID, TransitionInput{ExpectedVersion: started.Item.Version}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("manager complete error=%v", err)
	}
	completed, err := service.Complete(context.Background(), alice, created.Item.ID, TransitionInput{ExpectedVersion: started.Item.Version})
	if err != nil || completed.Item.Status != StatusCompleted {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	if _, err = service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: bob.ID, Reason: "late change", ExpectedVersion: completed.Item.Version}); !errors.Is(err, ErrCompletedImmutable) {
		t.Fatalf("completed assignment error=%v", err)
	}
}

func TestListOwnForcesAuthenticatedWorker(t *testing.T) {
	db := postgrestest.Open(t)
	now := databaseNowForWorkItems(t, db)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager}
	alice := auth.Principal{ID: seed.WorkerAliceID, Role: auth.RoleWorker}
	for index, workerID := range []string{seed.WorkerAliceID, seed.WorkerBobID} {
		created, err := service.Create(context.Background(), manager, CreateInput{Title: "Owned queue", IdempotencyKey: fmt.Sprintf("owned-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: workerID, ExpectedVersion: 1}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.ListOwn(context.Background(), alice, ListInput{AssigneeID: seed.WorkerBobID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].AssigneeID != alice.ID {
		t.Fatalf("page=%#v", page)
	}
}

func TestSameAssigneeNoopDoesNotEmailOrMutate(t *testing.T) {
	db := postgrestest.Open(t)
	now := databaseNowForWorkItems(t, db)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager}
	created, err := service.Create(context.Background(), manager, CreateInput{Title: "Same assignee", IdempotencyKey: "same-assignee"})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: seed.WorkerAliceID, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: seed.WorkerAliceID})
	if err != nil || !replayed.Idempotent || replayed.Item.Version != assigned.Item.Version {
		t.Fatalf("replayed=%#v err=%v", replayed, err)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_item_events WHERE work_item_id=$1`, created.Item.ID); got != 2 {
		t.Fatalf("events=%d", got)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM notification_deliveries WHERE work_item_id=$1`, created.Item.ID); got != 1 {
		t.Fatalf("deliveries=%d", got)
	}
}

func TestConcurrentTransitionsCreateOnePhysicalEvent(t *testing.T) {
	for _, test := range []struct {
		name      string
		target    Status
		eventType EventType
	}{
		{name: "start", target: StatusInProgress, eventType: EventMarkedInProgress},
		{name: "complete", target: StatusCompleted, eventType: EventCompleted},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := postgrestest.Open(t)
			now := databaseNowForWorkItems(t, db)
			_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
			service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
			manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager}
			alice := auth.Principal{ID: seed.WorkerAliceID, Role: auth.RoleWorker}
			created, err := service.Create(context.Background(), manager, CreateInput{Title: "Concurrent " + test.name, IdempotencyKey: "concurrent-" + test.name})
			if err != nil {
				t.Fatal(err)
			}
			assigned, err := service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: alice.ID, ExpectedVersion: 1})
			if err != nil {
				t.Fatal(err)
			}
			current := assigned.Item
			if test.target == StatusCompleted {
				started, startErr := service.Start(context.Background(), alice, created.Item.ID, TransitionInput{ExpectedVersion: current.Version})
				if startErr != nil {
					t.Fatal(startErr)
				}
				current = started.Item
			}
			start := make(chan struct{})
			results := make(chan MutationResult, 2)
			errorsCh := make(chan error, 2)
			for range 2 {
				go func() {
					<-start
					var result MutationResult
					var mutationErr error
					if test.target == StatusInProgress {
						result, mutationErr = service.Start(context.Background(), alice, created.Item.ID, TransitionInput{ExpectedVersion: current.Version})
					} else {
						result, mutationErr = service.Complete(context.Background(), alice, created.Item.ID, TransitionInput{ExpectedVersion: current.Version})
					}
					results <- result
					errorsCh <- mutationErr
				}()
			}
			close(start)
			idempotent := 0
			for range 2 {
				result, mutationErr := <-results, <-errorsCh
				if mutationErr != nil {
					t.Fatal(mutationErr)
				}
				if result.Idempotent {
					idempotent++
				}
			}
			if idempotent != 1 {
				t.Fatalf("idempotent results=%d", idempotent)
			}
			if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_item_events WHERE work_item_id=$1 AND event_type=$2`, created.Item.ID, string(test.eventType)); got != 1 {
				t.Fatalf("events=%d", got)
			}
		})
	}
}

func TestNotificationEnqueueUsesDatabaseClock(t *testing.T) {
	db := postgrestest.Open(t)
	dbNow := databaseNowForWorkItems(t, db)
	if _, err := seed.Apply(context.Background(), db, clock.Fixed{Time: dbNow}); err != nil {
		t.Fatal(err)
	}
	processFuture := dbNow.AddDate(20, 0, 0)
	service, _ := NewService(db, clock.Fixed{Time: processFuture}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya"}
	created, err := service.Create(context.Background(), manager, CreateInput{Title: "DB-clock enqueue", IdempotencyKey: "db-clock-enqueue"})
	if err != nil {
		t.Fatal(err)
	}
	before := databaseNowForWorkItems(t, db)
	if _, err = service.Assign(context.Background(), manager, created.Item.ID, AssignmentInput{AssigneeID: seed.WorkerAliceID, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	after := databaseNowForWorkItems(t, db)
	var nextAttempt, createdAt, updatedAt time.Time
	if err = db.QueryRowContext(context.Background(), `SELECT next_attempt_at,created_at,updated_at FROM notification_deliveries WHERE work_item_id=$1`, created.Item.ID).
		Scan(&nextAttempt, &createdAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	for name, timestamp := range map[string]time.Time{"next_attempt_at": nextAttempt, "created_at": createdAt, "updated_at": updatedAt} {
		if timestamp.Before(before) || timestamp.After(after) || timestamp.Equal(processFuture) {
			t.Fatalf("%s=%v before=%v after=%v process=%v", name, timestamp, before, after, processFuture)
		}
	}
	if !nextAttempt.Equal(createdAt) || !createdAt.Equal(updatedAt) {
		t.Fatalf("enqueue timestamps differ: next=%v created=%v updated=%v", nextAttempt, createdAt, updatedAt)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM notification_deliveries WHERE work_item_id=$1 AND next_attempt_at<=clock_timestamp()`, created.Item.ID); got != 1 {
		t.Fatalf("immediately eligible deliveries=%d", got)
	}
}

func databaseNowForWorkItems(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	var now time.Time
	if err := db.QueryRowContext(context.Background(), `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	return now.UTC()
}

func TestAssigneeOnlyKeysetPagination(t *testing.T) {
	db := postgrestest.Open(t)
	now := databaseNowForWorkItems(t, db)
	if _, err := seed.Apply(context.Background(), db, clock.Fixed{Time: now}); err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"c0000000-0000-4000-8000-000000000001",
		"c0000000-0000-4000-8000-000000000002",
		"c0000000-0000-4000-8000-000000000003",
		"c0000000-0000-4000-8000-000000000004",
		"c0000000-0000-4000-8000-000000000005",
	}
	for _, itemID := range ids {
		postgrestest.MustExec(t, db, `INSERT INTO work_items(id,title,description,status,creator_id,assignee_id,version,created_at,updated_at)
VALUES($1,$2,'','ASSIGNED',$3,$4,2,$5,$5)`, itemID, "Pagination "+itemID, seed.ManagerID, seed.WorkerAliceID, now)
	}
	service, _ := NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager}
	worker := auth.Principal{ID: seed.WorkerAliceID, Role: auth.RoleWorker}
	managerIDs := collectWorkItemPages(t, func(cursor string) (Page, error) {
		return service.ListManager(context.Background(), manager, ListInput{AssigneeID: seed.WorkerAliceID, Limit: 2, Cursor: cursor})
	})
	workerIDs := collectWorkItemPages(t, func(cursor string) (Page, error) {
		return service.ListOwn(context.Background(), worker, ListInput{AssigneeID: seed.WorkerBobID, Limit: 2, Cursor: cursor})
	})
	for label, got := range map[string][]string{"manager": managerIDs, "worker": workerIDs} {
		if len(got) != len(ids) {
			t.Fatalf("%s ids=%v", label, got)
		}
		seen := make(map[string]bool, len(got))
		for _, itemID := range got {
			if seen[itemID] {
				t.Fatalf("%s duplicate=%s", label, itemID)
			}
			seen[itemID] = true
		}
		for _, itemID := range ids {
			if !seen[itemID] {
				t.Fatalf("%s missing=%s ids=%v", label, itemID, got)
			}
		}
	}
}

func TestKeysetQueriesUseCoveringIndexes(t *testing.T) {
	db := postgrestest.Open(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(context.Background(), `SET LOCAL enable_seqscan=off; SET LOCAL enable_bitmapscan=off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(context.Background(), `EXPLAIN (COSTS OFF) `+workItemSelect+`
WHERE work_items.assignee_id=$1 ORDER BY work_items.created_at DESC,work_items.id DESC LIMIT 20`, seed.WorkerAliceID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "work_items_assignee_page_idx") {
		t.Fatalf("plan=%s", plan.String())
	}
}

func collectWorkItemPages(t *testing.T, list func(string) (Page, error)) []string {
	t.Helper()
	var result []string
	cursor := ""
	for {
		page, err := list(cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			result = append(result, item.ID)
		}
		if page.NextCursor == "" {
			return result
		}
		if page.NextCursor == cursor {
			t.Fatalf("cursor did not advance: %q", cursor)
		}
		cursor = page.NextCursor
	}
}
