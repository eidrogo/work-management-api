package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/platform/id"
	"github.com/eidrogo/work-management-api/internal/seed"
	"github.com/eidrogo/work-management-api/internal/testsupport/postgrestest"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

func TestConcurrentClaimAndReplay(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	itemService, _ := workitems.NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	manager := auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya"}
	created, _ := itemService.Create(context.Background(), manager, workitems.CreateInput{Title: "Claim test", IdempotencyKey: "claim-create"})
	_, err := itemService.Assign(context.Background(), manager, created.Item.ID, workitems.AssignmentInput{AssigneeID: seed.WorkerAliceID, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(db)
	start := make(chan struct{})
	results := make(chan []Delivery, 2)
	for _, workerID := range []string{"worker-a", "worker-b"} {
		workerID := workerID
		go func() {
			<-start
			claimed, claimErr := store.ClaimBatch(context.Background(), ClaimRequest{Now: now, WorkerID: workerID, BatchSize: 10, LeaseDuration: 30 * time.Second, MaxAttempts: 2})
			if claimErr != nil {
				results <- nil
				return
			}
			results <- claimed
		}()
	}
	close(start)
	claimed := append(<-results, <-results...)
	if len(claimed) != 1 {
		t.Fatalf("claimed=%d", len(claimed))
	}
	if err := store.Complete(context.Background(), Completion{Delivery: claimed[0], WorkerID: claimed[0].LockedBy, FinishedAt: now, Outcome: OutcomeFailed, ErrorMessage: "permanent"}); err != nil {
		t.Fatal(err)
	}
	replayed, err := store.Replay(context.Background(), claimed[0].ID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != StatusPending || replayed.DeliveryCycle != 2 || replayed.CycleAttemptCount != 0 {
		t.Fatalf("replayed=%#v", replayed)
	}
}
