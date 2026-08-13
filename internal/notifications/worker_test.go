package notifications

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"mime"

	"github.com/eidrogo/work-management-api/internal/workitems"
)

func TestWorkerClaimsImmediatelyBeforeEachSend(t *testing.T) {
	t.Parallel()
	recorder := &workerEventRecorder{}
	queue := &recordingQueue{recorder: recorder, deliveries: []Delivery{
		workerTestDelivery("11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111", 1),
		workerTestDelivery("22222222-2222-4222-8222-222222222222", "22222222-2222-4222-8222-222222222222", 1),
	}}
	sender := recordingSender{recorder: recorder}
	worker, err := NewWorker(queue, sender, slog.New(slog.NewTextHandler(io.Discard, nil)), WorkerConfig{
		WorkerID: "worker-a", BatchSize: 2, LeaseDuration: time.Minute, MaxAttempts: 3,
		BackoffBase: time.Second, BackoffMax: time.Minute, PollInterval: time.Second, Random: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessBatch(context.Background())
	if err != nil || processed != 2 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
	want := []string{
		"claim:11111111-1111-4111-8111-111111111111",
		"send:11111111-1111-4111-8111-111111111111",
		"complete:11111111-1111-4111-8111-111111111111",
		"claim:22222222-2222-4222-8222-222222222222",
		"send:22222222-2222-4222-8222-222222222222",
		"complete:22222222-2222-4222-8222-222222222222",
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%v", got)
	}
}

func TestWorkerRunUntilStoppedDrainsActiveAttemptBeforeReturning(t *testing.T) {
	t.Parallel()
	recorder := &workerEventRecorder{}
	queue := &recordingQueue{recorder: recorder, deliveries: []Delivery{
		workerTestDelivery("11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111", 1),
		workerTestDelivery("22222222-2222-4222-8222-222222222222", "22222222-2222-4222-8222-222222222222", 1),
	}}
	entered := make(chan struct{})
	release := make(chan struct{})
	worker, err := NewWorker(queue, blockingWorkerSender{recorder: recorder, entered: entered, release: release}, slog.New(slog.NewTextHandler(io.Discard, nil)), WorkerConfig{
		WorkerID: "worker-a", BatchSize: 2, LeaseDuration: time.Minute, MaxAttempts: 3,
		BackoffBase: time.Second, BackoffMax: time.Minute, PollInterval: time.Second, Random: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	claimCtx, stopClaims := context.WithCancel(context.Background())
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()
	result := make(chan error, 1)
	go func() { result <- worker.RunUntilStopped(claimCtx, attemptCtx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not begin the active send")
	}
	stopClaims()
	select {
	case err = <-result:
		t.Fatalf("worker returned before the active attempt drained: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case err = <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not return after the active attempt completed")
	}
	want := []string{
		"claim:11111111-1111-4111-8111-111111111111",
		"send:11111111-1111-4111-8111-111111111111",
		"complete:11111111-1111-4111-8111-111111111111",
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%v want=%v", got, want)
	}
}

func TestWorkerRunUntilStoppedPreservesCompletionErrorAfterClaimsStop(t *testing.T) {
	t.Parallel()
	for name, sentinel := range map[string]error{
		"sentinel":         errors.New("fenced completion failed"),
		"context canceled": context.Canceled,
	} {
		t.Run(name, func(t *testing.T) {
			assertCompletionErrorAfterClaimsStop(t, sentinel)
		})
	}
}

func assertCompletionErrorAfterClaimsStop(t *testing.T, sentinel error) {
	t.Helper()
	recorder := &workerEventRecorder{}
	queue := &recordingQueue{recorder: recorder, deliveries: []Delivery{
		workerTestDelivery("11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111", 1),
	}, completeError: sentinel}
	entered := make(chan struct{})
	release := make(chan struct{})
	worker, err := NewWorker(queue, blockingWorkerSender{recorder: recorder, entered: entered, release: release}, slog.New(slog.NewTextHandler(io.Discard, nil)), WorkerConfig{
		WorkerID: "worker-a", BatchSize: 1, LeaseDuration: time.Minute, MaxAttempts: 3,
		BackoffBase: time.Second, BackoffMax: time.Minute, PollInterval: time.Second, Random: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	claimCtx, stopClaims := context.WithCancel(context.Background())
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()
	result := make(chan error, 1)
	go func() { result <- worker.RunUntilStopped(claimCtx, attemptCtx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not begin the active send")
	}
	stopClaims()
	close(release)
	select {
	case err = <-result:
		if !errors.Is(err, sentinel) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not return after completion failed")
	}
}

func TestWorkerRunUntilStoppedPreservesCompletionErrorAtForcedCancellation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		sentinel error
	}{
		{name: "sentinel", sentinel: errors.New("database completion failed")},
		{name: "wrapped context canceled", sentinel: fmt.Errorf("database completion failed: %w", context.Canceled)},
		{name: "context canceled", sentinel: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertCompletionErrorAtForcedCancellation(t, test.sentinel)
		})
	}
}

func assertCompletionErrorAtForcedCancellation(t *testing.T, sentinel error) {
	t.Helper()
	recorder := &workerEventRecorder{}
	completionStarted := make(chan struct{})
	queue := &recordingQueue{recorder: recorder, deliveries: []Delivery{
		workerTestDelivery("11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111", 1),
	}, completeError: sentinel, completeStarted: completionStarted, completeAfterCancellation: true}
	worker, err := NewWorker(queue, recordingSender{recorder: recorder}, slog.New(slog.NewTextHandler(io.Discard, nil)), WorkerConfig{
		WorkerID: "worker-a", BatchSize: 1, LeaseDuration: time.Minute, MaxAttempts: 3,
		BackoffBase: time.Second, BackoffMax: time.Minute, PollInterval: time.Second, Random: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	claimCtx, stopClaims := context.WithCancel(context.Background())
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- worker.RunUntilStopped(claimCtx, attemptCtx) }()
	select {
	case <-completionStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not begin completion")
	}
	stopClaims()
	cancelAttempt()
	select {
	case err = <-result:
		if !errors.Is(err, sentinel) {
			t.Fatalf("completion error was masked: got %v want %v", err, sentinel)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not return after forced cancellation")
	}
}

func TestWorkerRunUntilStoppedPreservesSendErrorAtForcedCancellation(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("SMTP send failed during cancellation")
	recorder := &workerEventRecorder{}
	sendStarted := make(chan struct{})
	queue := &recordingQueue{recorder: recorder, deliveries: []Delivery{
		workerTestDelivery("11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111", 1),
	}}
	worker, err := NewWorker(queue, cancellationErrorWorkerSender{recorder: recorder, started: sendStarted, err: sentinel}, slog.New(slog.NewTextHandler(io.Discard, nil)), WorkerConfig{
		WorkerID: "worker-a", BatchSize: 1, LeaseDuration: time.Minute, MaxAttempts: 3,
		BackoffBase: time.Second, BackoffMax: time.Minute, PollInterval: time.Second, Random: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	claimCtx, stopClaims := context.WithCancel(context.Background())
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- worker.RunUntilStopped(claimCtx, attemptCtx) }()
	select {
	case <-sendStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not begin send")
	}
	stopClaims()
	cancelAttempt()
	select {
	case err = <-result:
		if !errors.Is(err, sentinel) {
			t.Fatalf("send error was masked: got %v want %v", err, sentinel)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not return after forced cancellation")
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, []string{
		"claim:11111111-1111-4111-8111-111111111111",
		"send:11111111-1111-4111-8111-111111111111",
	}) {
		t.Fatalf("events=%v", got)
	}
}

func TestWorkerRunUntilStoppedPreservesSendAndCompletionErrorsWhenCompletionObservesCancellation(t *testing.T) {
	t.Parallel()
	sendErr := errors.New("SMTP send failed")
	completionErr := context.Canceled
	recorder := &workerEventRecorder{}
	queue := &recordingQueue{recorder: recorder, deliveries: []Delivery{
		workerTestDelivery("11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111", 1),
	}, completeError: completionErr}
	worker, err := NewWorker(queue, immediateErrorWorkerSender{recorder: recorder, err: sendErr}, slog.New(slog.NewTextHandler(io.Discard, nil)), WorkerConfig{
		WorkerID: "worker-a", BatchSize: 1, LeaseDuration: time.Minute, MaxAttempts: 3,
		BackoffBase: time.Second, BackoffMax: time.Minute, PollInterval: time.Second, Random: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	claimCtx, stopClaims := context.WithCancel(context.Background())
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	queue.completeHook = func() {
		stopClaims()
		cancelAttempt()
	}
	result := make(chan error, 1)
	go func() { result <- worker.RunUntilStopped(claimCtx, attemptCtx) }()
	select {
	case err = <-result:
		if !errors.Is(err, sendErr) || !errors.Is(err, completionErr) {
			t.Fatalf("error=%v want send=%v completion=%v", err, sendErr, completionErr)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not return after completion observed cancellation")
	}
}

func TestEveryAcceptedTitleRendersAHeaderValidSubject(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		title       string
		wantSubject string
	}{
		{title: "Inspect loading dock", wantSubject: "New work assigned: Inspect loading dock"},
		{title: strings.Repeat("é", 200)},
		{title: strings.Repeat("😀", 200)},
	} {
		title := test.title
		input, err := workitems.ValidateCreateInput(workitems.CreateInput{Title: title, IdempotencyKey: "render-title"})
		if err != nil {
			t.Fatal(err)
		}
		delivery := workerTestDelivery("11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111", 1)
		delivery.Payload = []byte(fmt.Sprintf(`{"work_item_id":"%s","work_item_title":%q,"assignee_name":"Alice","assigned_by_name":"Maya","assigned_at":"2026-08-12T12:00:00Z"}`, delivery.ID, input.Title))
		message, err := renderAssignmentMessage(delivery)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(message.Subject) == "" || strings.ContainsAny(message.Subject, "\r\n") {
			t.Fatalf("subject=%q", message.Subject)
		}
		if test.wantSubject != "" && message.Subject != test.wantSubject {
			t.Fatalf("subject=%q want=%q", message.Subject, test.wantSubject)
		}
		if !strings.Contains(message.TextBody, "Title: "+title+"\n") {
			t.Fatalf("full title missing from body: %q", message.TextBody)
		}
		encoded := mime.QEncoding.Encode("UTF-8", message.Subject)
		if len("Subject: ")+len(encoded) > 998 {
			t.Fatalf("encoded subject line is %d octets", len("Subject: ")+len(encoded))
		}
	}
}

func TestNewWorkerRejectsInvalidWorkerIDBelowConfig(t *testing.T) {
	t.Parallel()
	for _, workerID := range []string{strings.Repeat("a", 256), "worker\nid", string([]byte{'w', 0xff})} {
		recorder := &workerEventRecorder{}
		worker, err := NewWorker(&recordingQueue{recorder: recorder}, recordingSender{recorder: recorder}, slog.New(slog.NewTextHandler(io.Discard, nil)), WorkerConfig{
			WorkerID: workerID, BatchSize: 1, LeaseDuration: time.Minute, MaxAttempts: 3,
			BackoffBase: time.Second, BackoffMax: time.Minute, PollInterval: time.Second, Random: func() float64 { return 0 },
		})
		if err == nil || worker != nil {
			t.Fatalf("worker ID %q unexpectedly accepted: worker=%v error=%v", workerID, worker, err)
		}
	}
}

type workerEventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (recorder *workerEventRecorder) add(event string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, event)
}
func (recorder *workerEventRecorder) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.events...)
}

type recordingQueue struct {
	recorder                  *workerEventRecorder
	deliveries                []Delivery
	completeError             error
	completeStarted           chan struct{}
	completeAfterCancellation bool
	completeHook              func()
}

func (queue *recordingQueue) ClaimNext(_ context.Context, request ClaimRequest) (Delivery, bool, error) {
	if len(queue.deliveries) == 0 {
		return Delivery{}, false, nil
	}
	delivery := queue.deliveries[0]
	queue.deliveries = queue.deliveries[1:]
	delivery.LockedBy = request.WorkerID
	queue.recorder.add("claim:" + delivery.ID)
	return delivery, true, nil
}
func (queue *recordingQueue) Complete(ctx context.Context, completion Completion) error {
	queue.recorder.add("complete:" + completion.DeliveryID)
	if queue.completeStarted != nil {
		close(queue.completeStarted)
	}
	if queue.completeHook != nil {
		queue.completeHook()
	}
	if queue.completeAfterCancellation {
		<-ctx.Done()
	}
	return queue.completeError
}

type recordingSender struct{ recorder *workerEventRecorder }

func (sender recordingSender) Send(_ context.Context, message Message) error {
	sender.recorder.add("send:" + message.MessageID)
	return nil
}

type blockingWorkerSender struct {
	recorder *workerEventRecorder
	entered  chan struct{}
	release  chan struct{}
}

type cancellationErrorWorkerSender struct {
	recorder *workerEventRecorder
	started  chan struct{}
	err      error
}

type immediateErrorWorkerSender struct {
	recorder *workerEventRecorder
	err      error
}

func (sender immediateErrorWorkerSender) Send(_ context.Context, message Message) error {
	sender.recorder.add("send:" + message.MessageID)
	return sender.err
}

func (sender cancellationErrorWorkerSender) Send(ctx context.Context, message Message) error {
	sender.recorder.add("send:" + message.MessageID)
	close(sender.started)
	<-ctx.Done()
	return sender.err
}

func (sender blockingWorkerSender) Send(ctx context.Context, message Message) error {
	sender.recorder.add("send:" + message.MessageID)
	close(sender.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-sender.release:
		return nil
	}
}

func workerTestDelivery(id, messageID string, attempt int) Delivery {
	payload := fmt.Sprintf(`{"work_item_id":"%s","work_item_title":"Task","assignee_name":"Alice","assigned_by_name":"Maya","assigned_at":"2026-08-12T12:00:00Z"}`, id)
	return Delivery{ID: id, WorkItemID: id, Payload: []byte(payload), MessageID: messageID, LeaseToken: "33333333-3333-4333-8333-333333333333", AttemptCount: attempt, CycleAttemptCount: attempt}
}
