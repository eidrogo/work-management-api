package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/platform/id"
	"github.com/eidrogo/work-management-api/internal/seed"
	"github.com/eidrogo/work-management-api/internal/testsupport/postgrestest"
)

const testLease = 2 * time.Second

func TestClaimNextClaimsExactlyOneAndUsesDatabaseTime(t *testing.T) {
	db := postgrestest.Open(t)
	deliveryIDs := insertDueDeliveries(t, db, 2)
	store, _ := NewStore(db)
	before := databaseNow(t, db)
	delivery, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-a", LeaseDuration: testLease, MaxAttempts: 3})
	after := databaseNow(t, db)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if delivery.ID != deliveryIDs[0] || !id.IsUUID(delivery.LeaseToken) {
		t.Fatalf("delivery=%#v", delivery)
	}
	if delivery.LastAttemptAt == nil || delivery.LastAttemptAt.Before(before) || delivery.LastAttemptAt.After(after) {
		t.Fatalf("last_attempt_at=%v before=%v after=%v", delivery.LastAttemptAt, before, after)
	}
	if delivery.LockedUntil == nil || delivery.LockedUntil.Sub(*delivery.LastAttemptAt) != testLease {
		t.Fatalf("locked_until=%v last_attempt_at=%v", delivery.LockedUntil, delivery.LastAttemptAt)
	}
	if delivery.NextAttemptAt != nil || delivery.Status != StatusProcessing {
		t.Fatalf("status=%s next_attempt_at=%v", delivery.Status, delivery.NextAttemptAt)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM notification_deliveries WHERE status='PENDING'`); got != 1 {
		t.Fatalf("pending=%d", got)
	}
}

func TestClaimNextRejectsInvalidWorkerIDBeforeSQLMutation(t *testing.T) {
	db := postgrestest.Open(t)
	ids := insertDueDeliveries(t, db, 1)
	store, _ := NewStore(db)
	for _, workerID := range []string{strings.Repeat("a", 256), "worker\nid", string([]byte{'w', 0xff})} {
		if _, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: workerID, LeaseDuration: testLease, MaxAttempts: 3}); !errors.Is(err, ErrInvalidInput) || found {
			t.Fatalf("worker ID %q found=%v error=%v", workerID, found, err)
		}
	}
	got, err := store.Get(context.Background(), ids[0])
	if err != nil || got.Status != StatusPending || got.AttemptCount != 0 || got.LockedBy != "" || got.LeaseToken != "" {
		t.Fatalf("delivery=%#v error=%v", got, err)
	}
}

func TestClaimNextSelectsEarliestEligibilityAcrossPendingAndExpired(t *testing.T) {
	db := postgrestest.Open(t)
	ids := insertDueDeliveries(t, db, 2)
	now := databaseNow(t, db)
	postgrestest.MustExec(t, db, `UPDATE notification_deliveries SET next_attempt_at=$1 WHERE id=$2`, now.Add(-time.Second), ids[0])
	postgrestest.MustExec(t, db, `UPDATE notification_deliveries SET status='PROCESSING',next_attempt_at=NULL,attempt_count=1,cycle_attempt_count=1,
locked_by='expired-worker',lease_token=gen_random_uuid(),locked_until=$1,last_attempt_at=$1 WHERE id=$2`, now.Add(-2*time.Second), ids[1])
	store, _ := NewStore(db)
	delivery, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-next", LeaseDuration: testLease, MaxAttempts: 3})
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if delivery.ID != ids[1] || delivery.AttemptCount != 2 {
		t.Fatalf("delivery=%#v want=%s", delivery, ids[1])
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM notification_attempts WHERE notification_id=$1 AND outcome='ABANDONED'`, ids[1]); got != 1 {
		t.Fatalf("abandoned attempts=%d", got)
	}
}

func TestEarlierClaimCandidateUsesCreatedAtAndIDForEqualEligibility(t *testing.T) {
	t.Parallel()
	due := time.Date(2026, 8, 13, 4, 0, 0, 0, time.UTC)
	created := due.Add(-time.Hour)
	pending := Delivery{ID: "22222222-2222-4222-8222-222222222222", NextAttemptAt: &due, CreatedAt: created}
	expired := Delivery{ID: "11111111-1111-4111-8111-111111111111", LockedUntil: &due, CreatedAt: created}
	got, found := earlierClaimCandidate(pending, true, expired, true)
	if !found || got.ID != expired.ID {
		t.Fatalf("delivery=%#v found=%v", got, found)
	}
}

func TestConcurrentClaimNextHasSingleWinner(t *testing.T) {
	db := postgrestest.Open(t)
	insertDueDeliveries(t, db, 1)
	store, _ := NewStore(db)
	start := make(chan struct{})
	type result struct {
		found bool
		err   error
	}
	results := make(chan result, 2)
	for _, workerID := range []string{"worker-a", "worker-b"} {
		workerID := workerID
		go func() {
			<-start
			_, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: workerID, LeaseDuration: testLease, MaxAttempts: 3})
			results <- result{found: found, err: err}
		}()
	}
	close(start)
	winners := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.found {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
}

func TestClaimNextTokensAreDistinct(t *testing.T) {
	db := postgrestest.Open(t)
	insertDueDeliveries(t, db, 2)
	store, _ := NewStore(db)
	first, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-a", LeaseDuration: testLease, MaxAttempts: 3})
	if err != nil || !found {
		t.Fatalf("first found=%v err=%v", found, err)
	}
	second, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-a", LeaseDuration: testLease, MaxAttempts: 3})
	if err != nil || !found {
		t.Fatalf("second found=%v err=%v", found, err)
	}
	if first.LeaseToken == second.LeaseToken || !id.IsUUID(first.LeaseToken) || !id.IsUUID(second.LeaseToken) {
		t.Fatalf("tokens=%q %q", first.LeaseToken, second.LeaseToken)
	}
}

func TestCompletionRequiresCurrentUnexpiredFence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, store *Store, db *sql.DB, delivery Delivery) (Completion, int)
	}{
		{name: "wrong owner", mutate: func(_ *testing.T, _ *Store, _ *sql.DB, delivery Delivery) (Completion, int) {
			return sentCompletion(delivery, "worker-b"), 0
		}},
		{name: "wrong token", mutate: func(_ *testing.T, _ *Store, _ *sql.DB, delivery Delivery) (Completion, int) {
			completion := sentCompletion(delivery, "worker-a")
			completion.LeaseToken = "99999999-9999-4999-8999-999999999999"
			return completion, 0
		}},
		{name: "wrong attempt", mutate: func(_ *testing.T, _ *Store, _ *sql.DB, delivery Delivery) (Completion, int) {
			completion := sentCompletion(delivery, "worker-a")
			completion.AttemptNumber++
			return completion, 0
		}},
		{name: "expired token", mutate: func(_ *testing.T, _ *Store, _ *sql.DB, delivery Delivery) (Completion, int) {
			time.Sleep(35 * time.Millisecond)
			return sentCompletion(delivery, "worker-a"), 0
		}},
		{name: "old token after reclaim", mutate: func(t *testing.T, store *Store, _ *sql.DB, delivery Delivery) (Completion, int) {
			time.Sleep(35 * time.Millisecond)
			if _, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-b", LeaseDuration: testLease, MaxAttempts: 3}); err != nil || !found {
				t.Fatalf("reclaim found=%v err=%v", found, err)
			}
			return sentCompletion(delivery, "worker-a"), 1
		}},
		{name: "already completed", mutate: func(t *testing.T, store *Store, _ *sql.DB, delivery Delivery) (Completion, int) {
			completion := sentCompletion(delivery, "worker-a")
			if err := store.Complete(context.Background(), completion); err != nil {
				t.Fatal(err)
			}
			return completion, 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := postgrestest.Open(t)
			insertDueDeliveries(t, db, 1)
			store, _ := NewStore(db)
			lease := testLease
			if test.name == "expired token" || test.name == "old token after reclaim" {
				lease = 10 * time.Millisecond
			}
			delivery, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-a", LeaseDuration: lease, MaxAttempts: 3})
			if err != nil || !found {
				t.Fatalf("claim found=%v err=%v", found, err)
			}
			completion, attemptsBefore := test.mutate(t, store, db, delivery)
			if err = store.Complete(context.Background(), completion); !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("error=%v", err)
			}
			if got := postgrestest.Count(t, db, `SELECT count(*) FROM notification_attempts WHERE notification_id=$1`, delivery.ID); got != attemptsBefore {
				t.Fatalf("attempts=%d want=%d", got, attemptsBefore)
			}
		})
	}
}

func TestExpiredLeaseIsAbandonedAndReclaimed(t *testing.T) {
	db := postgrestest.Open(t)
	insertDueDeliveries(t, db, 1)
	store, _ := NewStore(db)
	first, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-a", LeaseDuration: 10 * time.Millisecond, MaxAttempts: 3})
	if err != nil || !found {
		t.Fatalf("first found=%v err=%v", found, err)
	}
	time.Sleep(35 * time.Millisecond)
	before := databaseNow(t, db)
	second, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-b", LeaseDuration: testLease, MaxAttempts: 3})
	after := databaseNow(t, db)
	if err != nil || !found {
		t.Fatalf("second found=%v err=%v", found, err)
	}
	if second.ID != first.ID || second.MessageID != first.MessageID || second.AttemptCount != 2 || second.LeaseToken == first.LeaseToken || second.LockedBy != "worker-b" {
		t.Fatalf("reclaimed=%#v first=%#v", second, first)
	}
	got, err := store.Get(context.Background(), first.ID)
	if err != nil || len(got.Attempts) != 1 {
		t.Fatalf("delivery=%#v err=%v", got, err)
	}
	attempt := got.Attempts[0]
	if attempt.Outcome != OutcomeAbandoned || attempt.ErrorCode != FailureLeaseExpired || attempt.ErrorMessage != SafeFailureMessage(FailureLeaseExpired) || attempt.FinishedAt.Before(before) || attempt.FinishedAt.After(after) {
		t.Fatalf("attempt=%#v before=%v after=%v", attempt, before, after)
	}
}

func TestExpiredFinalAttemptBecomesFailedWithoutSend(t *testing.T) {
	db := postgrestest.Open(t)
	ids := insertDueDeliveries(t, db, 1)
	store, _ := NewStore(db)
	if _, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-a", LeaseDuration: 10 * time.Millisecond, MaxAttempts: 1}); err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	time.Sleep(35 * time.Millisecond)
	if _, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-b", LeaseDuration: testLease, MaxAttempts: 1}); err != nil || found {
		t.Fatalf("housekeeping found=%v err=%v", found, err)
	}
	got, err := store.Get(context.Background(), ids[0])
	if err != nil || got.Status != StatusFailed || got.LastErrorCode != FailureRetryLimit || got.LastError != SafeFailureMessage(FailureRetryLimit) || len(got.Attempts) != 1 || got.Attempts[0].Outcome != OutcomeAbandoned {
		t.Fatalf("delivery=%#v err=%v", got, err)
	}
}

func TestCompletionUsesDatabaseTimeAndRetryDelay(t *testing.T) {
	db := postgrestest.Open(t)
	insertDueDeliveries(t, db, 1)
	store, _ := NewStore(db)
	delivery, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-a", LeaseDuration: testLease, MaxAttempts: 3})
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	delay := 250 * time.Millisecond
	before := databaseNow(t, db)
	err = store.Complete(context.Background(), Completion{DeliveryID: delivery.ID, WorkerID: "worker-a", LeaseToken: delivery.LeaseToken, AttemptNumber: delivery.AttemptCount, Outcome: OutcomeRetryScheduled, RetryAfter: delay, FailureCode: FailureSMTPConnection})
	after := databaseNow(t, db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), delivery.ID)
	if err != nil || len(got.Attempts) != 1 {
		t.Fatalf("delivery=%#v err=%v", got, err)
	}
	if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) || got.Attempts[0].FinishedAt.Before(before) || got.Attempts[0].FinishedAt.After(after) {
		t.Fatalf("updated=%v finished=%v before=%v after=%v", got.UpdatedAt, got.Attempts[0].FinishedAt, before, after)
	}
	if !got.UpdatedAt.Equal(got.Attempts[0].FinishedAt) {
		t.Fatalf("completion timestamps differ: updated=%v finished=%v", got.UpdatedAt, got.Attempts[0].FinishedAt)
	}
	if got.NextAttemptAt == nil || got.NextAttemptAt.Before(before.Add(delay)) || got.NextAttemptAt.After(after.Add(delay)) {
		t.Fatalf("next_attempt_at=%v", got.NextAttemptAt)
	}
	if !got.NextAttemptAt.Equal(got.UpdatedAt.Add(delay)) {
		t.Fatalf("retry delay=%v want=%v", got.NextAttemptAt.Sub(got.UpdatedAt), delay)
	}
	if got.LastErrorCode != FailureSMTPConnection || got.LastError != SafeFailureMessage(FailureSMTPConnection) {
		t.Fatalf("code=%q error=%q", got.LastErrorCode, got.LastError)
	}
}

func TestSlowWorkerDoesNotReserveSecondWorkersDelivery(t *testing.T) {
	db := postgrestest.Open(t)
	insertDueDeliveries(t, db, 2)
	store, _ := NewStore(db)
	entered := make(chan struct{})
	release := make(chan struct{})
	slowSender := &blockingSender{entered: entered, release: release}
	workerA := newIntegrationWorker(t, store, slowSender, "worker-a", 2, 3)
	workerB := newIntegrationWorker(t, store, &countingSender{}, "worker-b", 1, 3)
	type result struct {
		processed int
		err       error
	}
	aResult := make(chan result, 1)
	go func() {
		processed, err := workerA.ProcessBatch(context.Background())
		aResult <- result{processed: processed, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker A did not begin its send")
	}
	processed, err := workerB.ProcessBatch(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("worker B processed=%d err=%v", processed, err)
	}
	close(release)
	resultA := <-aResult
	if resultA.err != nil || resultA.processed != 1 {
		t.Fatalf("worker A processed=%d err=%v", resultA.processed, resultA.err)
	}
}

func TestWorkerDrainPersistsActiveCompletionBeforeReturning(t *testing.T) {
	db := postgrestest.Open(t)
	ids := insertDueDeliveries(t, db, 1)
	store, _ := NewStore(db)
	entered := make(chan struct{})
	release := make(chan struct{})
	worker := newIntegrationWorker(t, store, &blockingSender{entered: entered, release: release}, "worker-drain", 1, 3)
	claimCtx, stopClaims := context.WithCancel(context.Background())
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()
	result := make(chan error, 1)
	go func() { result <- worker.RunUntilStopped(claimCtx, attemptCtx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not begin send")
	}
	stopClaims()
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker drain did not return")
	}
	got, err := store.Get(context.Background(), ids[0])
	if err != nil || got.Status != StatusSent || len(got.Attempts) != 1 || got.Attempts[0].Outcome != OutcomeSent {
		t.Fatalf("delivery=%#v error=%v", got, err)
	}
}

func TestWorkerForcedDrainIsBoundedAndLeaseRecovers(t *testing.T) {
	db := postgrestest.Open(t)
	ids := insertDueDeliveries(t, db, 1)
	store, _ := NewStore(db)
	entered := make(chan struct{})
	release := make(chan struct{})
	worker, err := NewWorker(store, &blockingSender{entered: entered, release: release}, slog.New(slog.NewTextHandler(io.Discard, nil)), WorkerConfig{
		WorkerID: "worker-forced-drain", BatchSize: 1, LeaseDuration: 80 * time.Millisecond, MaxAttempts: 3,
		BackoffBase: 100 * time.Millisecond, BackoffMax: time.Second, PollInterval: time.Second, Random: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	claimCtx, stopClaims := context.WithCancel(context.Background())
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- worker.RunUntilStopped(claimCtx, attemptCtx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not begin send")
	}
	stopClaims()
	started := time.Now()
	cancelAttempt()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("forced cancellation was not bounded")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("forced cancellation elapsed=%s", elapsed)
	}
	got, err := store.Get(context.Background(), ids[0])
	if err != nil || got.Status != StatusProcessing || len(got.Attempts) != 0 {
		t.Fatalf("delivery before recovery=%#v error=%v", got, err)
	}
	time.Sleep(100 * time.Millisecond)
	recovered, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-recovery", LeaseDuration: testLease, MaxAttempts: 3})
	if err != nil || !found || recovered.ID != ids[0] || recovered.AttemptCount != 2 {
		t.Fatalf("recovered=%#v found=%v error=%v", recovered, found, err)
	}
	got, err = store.Get(context.Background(), ids[0])
	if err != nil || len(got.Attempts) != 1 || got.Attempts[0].Outcome != OutcomeAbandoned {
		t.Fatalf("delivery after recovery=%#v error=%v", got, err)
	}
}

func TestWorkerRetryAndExhaustionUseSafeCodes(t *testing.T) {
	sentinel := errors.New("provider-secret user@example.test")
	tests := []struct {
		name        string
		sendError   error
		maxAttempts int
		wantStatus  Status
		wantCode    FailureCode
	}{
		{name: "retryable", sendError: Retryable(FailureSMTPConnection, sentinel), maxAttempts: 3, wantStatus: StatusPending, wantCode: FailureSMTPConnection},
		{name: "permanent", sendError: Permanent(FailureSMTPRecipientRejected, sentinel), maxAttempts: 3, wantStatus: StatusFailed, wantCode: FailureSMTPRecipientRejected},
		{name: "retry exhaustion", sendError: Retryable(FailureSMTPTemporary, sentinel), maxAttempts: 1, wantStatus: StatusFailed, wantCode: FailureSMTPTemporary},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := postgrestest.Open(t)
			ids := insertDueDeliveries(t, db, 1)
			store, _ := NewStore(db)
			worker := newIntegrationWorker(t, store, &countingSender{err: test.sendError}, "worker-a", 1, test.maxAttempts)
			if processed, err := worker.ProcessBatch(context.Background()); err != nil || processed != 1 {
				t.Fatalf("processed=%d err=%v", processed, err)
			}
			got, err := store.Get(context.Background(), ids[0])
			if err != nil || got.Status != test.wantStatus || got.LastErrorCode != test.wantCode || strings.Contains(got.LastError, "provider-secret") || len(got.Attempts) != 1 || strings.Contains(got.Attempts[0].ErrorMessage, "provider-secret") {
				t.Fatalf("delivery=%#v err=%v", got, err)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "provider-secret") || strings.Contains(string(encoded), got.LeaseToken) && got.LeaseToken != "" {
				t.Fatalf("unsafe JSON=%s", encoded)
			}
		})
	}
}

func TestReplayAtomicallyAppendsAudit(t *testing.T) {
	db := postgrestest.Open(t)
	store, deliveryID := insertFailedDelivery(t, db, 1)
	request := ReplayRequest{NotificationID: deliveryID, ActorID: "manager-1", RequestID: "request-http-1", Channel: ReplayChannelHTTP, Reason: "  Provider issue resolved  "}
	before := databaseNow(t, db)
	replayed, err := store.Replay(context.Background(), request)
	after := databaseNow(t, db)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != StatusPending || replayed.DeliveryCycle != 2 || replayed.CycleAttemptCount != 0 || replayed.NextAttemptAt == nil || !replayed.UpdatedAt.Equal(*replayed.NextAttemptAt) || replayed.UpdatedAt.Before(before) || replayed.UpdatedAt.After(after) {
		t.Fatalf("replayed=%#v before=%v after=%v", replayed, before, after)
	}
	got, err := store.Get(context.Background(), deliveryID)
	if err != nil || len(got.Replays) != 1 {
		t.Fatalf("delivery=%#v err=%v", got, err)
	}
	event := got.Replays[0]
	if event.DeliveryCycle != 2 || event.ActorID != "manager-1" || event.RequestID != "request-http-1" || event.Channel != ReplayChannelHTTP || event.Reason != "Provider issue resolved" || !event.OccurredAt.Equal(replayed.UpdatedAt) {
		t.Fatalf("event=%#v replayed=%#v", event, replayed)
	}
}

func TestReplayAuditFailureRollsBackState(t *testing.T) {
	db := postgrestest.Open(t)
	store, deliveryID := insertFailedDelivery(t, db, 1)
	postgrestest.MustExec(t, db, `CREATE FUNCTION reject_test_replay_event() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'reject audit'; END; $$ LANGUAGE plpgsql`)
	postgrestest.MustExec(t, db, `CREATE TRIGGER reject_test_replay_event BEFORE INSERT ON notification_replay_events FOR EACH ROW EXECUTE FUNCTION reject_test_replay_event()`)
	_, err := store.Replay(context.Background(), ReplayRequest{NotificationID: deliveryID, ActorID: "manager", RequestID: "request-audit-fail", Channel: ReplayChannelHTTP, Reason: "retry"})
	if err == nil {
		t.Fatal("replay unexpectedly succeeded")
	}
	got, getErr := store.Get(context.Background(), deliveryID)
	if getErr != nil || got.Status != StatusFailed || got.DeliveryCycle != 1 || len(got.Replays) != 0 {
		t.Fatalf("delivery=%#v err=%v", got, getErr)
	}
}

func TestConcurrentReplayCreatesOneCycleAndEvent(t *testing.T) {
	db := postgrestest.Open(t)
	store, deliveryID := insertFailedDelivery(t, db, 1)
	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	for _, requestID := range []string{"request-concurrent-a", "request-concurrent-b"} {
		requestID := requestID
		go func() {
			<-start
			_, err := store.Replay(context.Background(), ReplayRequest{NotificationID: deliveryID, ActorID: "manager", RequestID: requestID, Channel: ReplayChannelHTTP, Reason: "retry"})
			errorsCh <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-errorsCh
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrNotReplayable):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	got, err := store.Get(context.Background(), deliveryID)
	if err != nil || got.DeliveryCycle != 2 || len(got.Replays) != 1 {
		t.Fatalf("delivery=%#v err=%v", got, err)
	}
}

func TestReplayStopsAtCycleLimit(t *testing.T) {
	db := postgrestest.Open(t)
	store, deliveryID := insertFailedDelivery(t, db, MaxDeliveryCycles)
	_, err := store.Replay(context.Background(), ReplayRequest{NotificationID: deliveryID, ActorID: "manager", RequestID: "request-limit", Channel: ReplayChannelHTTP, Reason: "retry"})
	if !errors.Is(err, ErrReplayLimitReached) {
		t.Fatalf("error=%v", err)
	}
	got, getErr := store.Get(context.Background(), deliveryID)
	if getErr != nil || got.Status != StatusFailed || got.DeliveryCycle != MaxDeliveryCycles || len(got.Replays) != 0 {
		t.Fatalf("delivery=%#v err=%v", got, getErr)
	}
}

func TestReplayEventsAreAppendOnlyAndReturnedByGet(t *testing.T) {
	db := postgrestest.Open(t)
	store, deliveryID := insertFailedDelivery(t, db, 1)
	if _, err := store.Replay(context.Background(), ReplayRequest{NotificationID: deliveryID, ActorID: "manager-a", RequestID: "request-replay-2", Channel: ReplayChannelHTTP, Reason: "first replay"}); err != nil {
		t.Fatal(err)
	}
	delivery, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-replay", LeaseDuration: time.Second, MaxAttempts: 3})
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	if err = store.Complete(context.Background(), Completion{DeliveryID: delivery.ID, WorkerID: "worker-replay", LeaseToken: delivery.LeaseToken, AttemptNumber: delivery.AttemptCount, Outcome: OutcomeFailed, FailureCode: FailureSMTPRecipientRejected}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Replay(context.Background(), ReplayRequest{NotificationID: deliveryID, ActorID: "operator-b", RequestID: "request-replay-3", Channel: ReplayChannelCLI, Reason: "second replay"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), deliveryID)
	if err != nil || len(got.Replays) != 2 || got.Replays[0].Sequence >= got.Replays[1].Sequence || got.Replays[0].DeliveryCycle != 2 || got.Replays[1].DeliveryCycle != 3 {
		t.Fatalf("delivery=%#v err=%v", got, err)
	}
	if _, err = db.ExecContext(context.Background(), `UPDATE notification_replay_events SET reason='mutated' WHERE notification_id=$1`, deliveryID); err == nil {
		t.Fatal("replay update unexpectedly succeeded")
	}
	if _, err = db.ExecContext(context.Background(), `DELETE FROM notification_replay_events WHERE notification_id=$1`, deliveryID); err == nil {
		t.Fatal("replay delete unexpectedly succeeded")
	}
}

func insertFailedDelivery(t *testing.T, db *sql.DB, cycle int) (*Store, string) {
	t.Helper()
	ids := insertDueDeliveries(t, db, 1)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	delivery, found, err := store.ClaimNext(context.Background(), ClaimRequest{WorkerID: "worker-fail", LeaseDuration: time.Second, MaxAttempts: 3})
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	if err = store.Complete(context.Background(), Completion{DeliveryID: delivery.ID, WorkerID: "worker-fail", LeaseToken: delivery.LeaseToken, AttemptNumber: delivery.AttemptCount, Outcome: OutcomeFailed, FailureCode: FailureSMTPRecipientRejected}); err != nil {
		t.Fatal(err)
	}
	if cycle != 1 {
		postgrestest.MustExec(t, db, `UPDATE notification_deliveries SET delivery_cycle=$1 WHERE id=$2`, cycle, ids[0])
	}
	return store, ids[0]
}

func TestUnfilteredNotificationKeysetPagination(t *testing.T) {
	db := postgrestest.Open(t)
	ids := insertDueDeliveries(t, db, 25)
	tied := databaseNow(t, db)
	postgrestest.MustExec(t, db, `UPDATE notification_deliveries SET created_at=$1`, tied)
	store, _ := NewStore(db)
	var got []string
	cursor := ""
	for {
		page, err := store.List(context.Background(), ListInput{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, delivery := range page.Items {
			got = append(got, delivery.ID)
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			t.Fatalf("cursor did not advance: %q", cursor)
		}
		cursor = page.NextCursor
	}
	if len(got) != len(ids) {
		t.Fatalf("ids=%v", got)
	}
	seen := make(map[string]bool, len(got))
	for _, deliveryID := range got {
		if seen[deliveryID] {
			t.Fatalf("duplicate=%s", deliveryID)
		}
		seen[deliveryID] = true
	}
	for _, deliveryID := range ids {
		if !seen[deliveryID] {
			t.Fatalf("missing=%s ids=%v", deliveryID, got)
		}
	}
}

func TestFilteredNotificationKeysetPaginationAcrossTiedTimestamps(t *testing.T) {
	db := postgrestest.Open(t)
	ids := insertDueDeliveries(t, db, 25)
	tied := databaseNow(t, db)
	postgrestest.MustExec(t, db, `UPDATE notification_deliveries SET created_at=$1`, tied)
	store, _ := NewStore(db)
	for name, input := range map[string]ListInput{
		"status":    {Status: StatusPending, Limit: 3},
		"work item": {WorkItemID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Limit: 3},
	} {
		t.Run(name, func(t *testing.T) {
			got := collectNotificationPages(t, store, input)
			assertSameNotificationIDs(t, got, ids)
		})
	}
}

func collectNotificationPages(t *testing.T, store *Store, input ListInput) []string {
	t.Helper()
	var got []string
	for {
		page, err := store.List(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		for _, delivery := range page.Items {
			got = append(got, delivery.ID)
		}
		if page.NextCursor == "" {
			return got
		}
		if page.NextCursor == input.Cursor {
			t.Fatalf("cursor did not advance: %q", input.Cursor)
		}
		input.Cursor = page.NextCursor
	}
}

func assertSameNotificationIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids=%v", got)
	}
	seen := make(map[string]bool, len(got))
	for _, deliveryID := range got {
		if seen[deliveryID] {
			t.Fatalf("duplicate=%s", deliveryID)
		}
		seen[deliveryID] = true
	}
	for _, deliveryID := range want {
		if !seen[deliveryID] {
			t.Fatalf("missing=%s ids=%v", deliveryID, got)
		}
	}
}

func TestClaimCandidatePlansUseBoundedPartialIndexes(t *testing.T) {
	db := postgrestest.Open(t)
	insertLargeMixedNotificationBacklog(t, db, 12000)
	now := databaseNow(t, db)
	assertJSONPlanUsesIndexWithoutScanOrSort(t, db, pendingClaimQuery, []any{now}, "notification_deliveries_claim_idx")
	assertJSONPlanUsesIndexWithoutScanOrSort(t, db, expiredClaimQuery, []any{now}, "notification_deliveries_expired_lease_idx")
}

func TestNotificationListPlansUseMatchingIndexesWithoutSorts(t *testing.T) {
	db := postgrestest.Open(t)
	insertLargeTiedNotificationBacklog(t, db, 12000)
	tied := databaseNow(t, db)
	postgrestest.MustExec(t, db, `UPDATE notification_deliveries SET created_at=$1`, tied)
	postgrestest.MustExec(t, db, `UPDATE notification_deliveries SET status='SENT',next_attempt_at=NULL,sent_at=clock_timestamp()
WHERE (get_byte(uuid_send(id),15) % 3)=0`)
	postgrestest.MustExec(t, db, `ANALYZE notification_deliveries`)
	for _, test := range []struct {
		input ListInput
		index string
	}{
		{input: ListInput{Limit: 20}, index: "notification_deliveries_page_idx"},
		{input: ListInput{Status: StatusSent, Limit: 20}, index: "notification_deliveries_status_page_idx"},
		{input: ListInput{WorkItemID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Limit: 20}, index: "notification_deliveries_work_item_idx"},
	} {
		query, args, err := notificationListQuery(test.input)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONPlanUsesIndexWithoutScanOrSort(t, db, query, args, test.index)
	}
}

func insertDueDeliveries(t *testing.T, db *sql.DB, count int) []string {
	t.Helper()
	now := databaseNow(t, db)
	if _, err := seed.Apply(context.Background(), db, clock.Fixed{Time: now}); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, count)
	for index := range count {
		deliveryID, err := id.NewUUID()
		if err != nil {
			t.Fatal(err)
		}
		eventPublicID, err := id.NewUUID()
		if err != nil {
			t.Fatal(err)
		}
		var eventSequence int64
		if err = db.QueryRowContext(context.Background(), `INSERT INTO work_item_events(public_id,work_item_id,actor_id,event_type,from_status,to_status,new_assignee_id,occurred_at)
VALUES($1,'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',$2,'WORK_ITEM_ASSIGNED','UNASSIGNED','ASSIGNED',$3,clock_timestamp()) RETURNING id`, eventPublicID, seed.ManagerID, seed.WorkerAliceID).Scan(&eventSequence); err != nil {
			t.Fatal(err)
		}
		payload := `{"work_item_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","work_item_title":"Task","assignee_name":"Alice","assigned_by_name":"Maya","assigned_at":"2026-08-12T12:00:00Z"}`
		if _, err = db.ExecContext(context.Background(), `INSERT INTO notification_deliveries(
id,work_item_id,assignment_event_id,recipient_email,recipient_name,event_type,payload,status,attempt_count,delivery_cycle,cycle_attempt_count,next_attempt_at,message_id,created_at,updated_at,deduplication_key)
VALUES($1,'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',$2,'alice@example.test','Alice','WORK_ITEM_ASSIGNED_EMAIL',$3,'PENDING',0,1,0,clock_timestamp()-interval '1 second',$4,clock_timestamp()+$5*interval '1 microsecond',clock_timestamp(),$6)`, deliveryID, eventSequence, payload, "<"+deliveryID+"@example.test>", index, "notification-test:"+deliveryID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, deliveryID)
	}
	return ids
}

func insertLargeMixedNotificationBacklog(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	insertLargeTiedNotificationBacklog(t, db, count/2)
	insertLargeTiedNotificationBacklogForWorkItem(t, db, count/2, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "mixed")
	postgrestest.MustExec(t, db, `UPDATE notification_deliveries
SET status='PROCESSING',next_attempt_at=NULL,attempt_count=1,cycle_attempt_count=1,
locked_by='expired-fixture',lease_token=gen_random_uuid(),locked_until=clock_timestamp()-interval '1 second',last_attempt_at=clock_timestamp()-interval '2 seconds'
WHERE work_item_id='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'`)
	postgrestest.MustExec(t, db, `ANALYZE notification_deliveries`)
}

func insertLargeTiedNotificationBacklog(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	now := databaseNow(t, db)
	if _, err := seed.Apply(context.Background(), db, clock.Fixed{Time: now}); err != nil {
		t.Fatal(err)
	}
	targetCount := count / 12
	insertLargeTiedNotificationBacklogForWorkItem(t, db, targetCount, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "page-a")
	insertLargeTiedNotificationBacklogForWorkItem(t, db, count-targetCount, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "page-b")
	postgrestest.MustExec(t, db, `ANALYZE notification_deliveries`)
}

func insertLargeTiedNotificationBacklogForWorkItem(t *testing.T, db *sql.DB, count int, workItemID, fixture string) {
	t.Helper()
	postgrestest.MustExec(t, db, `WITH source AS (
SELECT generate_series(1,$1)::bigint AS n
), events AS (
INSERT INTO work_item_events(public_id,work_item_id,actor_id,event_type,from_status,to_status,new_assignee_id,occurred_at)
SELECT md5($4||'-event-'||n::text)::uuid,$5,$2,
'WORK_ITEM_ASSIGNED','UNASSIGNED','ASSIGNED',$3,clock_timestamp() FROM source RETURNING id,public_id
)
INSERT INTO notification_deliveries(
id,work_item_id,assignment_event_id,recipient_email,recipient_name,event_type,payload,status,attempt_count,delivery_cycle,cycle_attempt_count,next_attempt_at,message_id,created_at,updated_at,deduplication_key)
SELECT md5($4||'-delivery-'||events.public_id::text)::uuid,$5,events.id,
'alice@example.test','Alice','WORK_ITEM_ASSIGNED_EMAIL','{"work_item_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","work_item_title":"Task","assignee_name":"Alice","assigned_by_name":"Maya","assigned_at":"2026-08-12T12:00:00Z"}'::jsonb,
'PENDING',0,1,0,clock_timestamp()-interval '1 second','<'||events.public_id::text||'@example.test>',clock_timestamp(),clock_timestamp(),'plan:'||events.public_id::text
FROM events`, count, seed.ManagerID, seed.WorkerAliceID, fixture, workItemID)
}

func assertJSONPlanUsesIndexWithoutScanOrSort(t *testing.T, db *sql.DB, query string, args []any, expectedIndex string) {
	t.Helper()
	var raw []byte
	if err := db.QueryRowContext(context.Background(), `EXPLAIN (FORMAT JSON, COSTS OFF) `+query, args...).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root []map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("decode plan: %v plan=%s", err, raw)
	}
	foundIndex := false
	var visit func(map[string]any)
	visit = func(node map[string]any) {
		nodeType, _ := node["Node Type"].(string)
		switch nodeType {
		case "Seq Scan", "Sort", "Incremental Sort":
			t.Fatalf("forbidden %s in plan=%s", nodeType, raw)
		}
		if indexName, _ := node["Index Name"].(string); indexName == expectedIndex {
			foundIndex = true
		}
		if children, ok := node["Plans"].([]any); ok {
			for _, child := range children {
				visit(child.(map[string]any))
			}
		}
	}
	visit(root[0]["Plan"].(map[string]any))
	if !foundIndex {
		t.Fatalf("missing index %s in plan=%s", expectedIndex, raw)
	}
}

func databaseNow(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	var now time.Time
	if err := db.QueryRowContext(context.Background(), `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	return now.UTC()
}

func sentCompletion(delivery Delivery, workerID string) Completion {
	return Completion{DeliveryID: delivery.ID, WorkerID: workerID, LeaseToken: delivery.LeaseToken, AttemptNumber: delivery.AttemptCount, Outcome: OutcomeSent}
}

type blockingSender struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (sender *blockingSender) Send(ctx context.Context, _ Message) error {
	sender.once.Do(func() { close(sender.entered) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-sender.release:
		return nil
	}
}

type countingSender struct {
	mu    sync.Mutex
	count int
	err   error
}

func (sender *countingSender) Send(context.Context, Message) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.count++
	return sender.err
}

func newIntegrationWorker(t *testing.T, queue Queue, sender Sender, workerID string, batchSize, maxAttempts int) *Worker {
	t.Helper()
	worker, err := NewWorker(queue, sender, slog.New(slog.NewTextHandler(io.Discard, nil)), WorkerConfig{
		WorkerID: workerID, BatchSize: batchSize, LeaseDuration: time.Second, MaxAttempts: maxAttempts,
		BackoffBase: 100 * time.Millisecond, BackoffMax: time.Second, PollInterval: time.Second, Random: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
