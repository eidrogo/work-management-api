package notifications

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"
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
	recorder   *workerEventRecorder
	deliveries []Delivery
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
func (queue *recordingQueue) Complete(_ context.Context, completion Completion) error {
	queue.recorder.add("complete:" + completion.DeliveryID)
	return nil
}

type recordingSender struct{ recorder *workerEventRecorder }

func (sender recordingSender) Send(_ context.Context, message Message) error {
	sender.recorder.add("send:" + message.MessageID)
	return nil
}

func workerTestDelivery(id, messageID string, attempt int) Delivery {
	payload := fmt.Sprintf(`{"work_item_id":"%s","work_item_title":"Task","assignee_name":"Alice","assigned_by_name":"Maya","assigned_at":"2026-08-12T12:00:00Z"}`, id)
	return Delivery{ID: id, WorkItemID: id, Payload: []byte(payload), MessageID: messageID, LeaseToken: "33333333-3333-4333-8333-333333333333", AttemptCount: attempt, CycleAttemptCount: attempt}
}
