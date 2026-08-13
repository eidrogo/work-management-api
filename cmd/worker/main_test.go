package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

func TestRunRejectsInvalidWorkerIDBeforeOpeningDependencies(t *testing.T) {
	for key, value := range map[string]string{
		"APP_ENV": "development", "DATABASE_URL": "postgres://127.0.0.1:1/work?sslmode=disable", "DATABASE_MAX_CONNS": "1",
		"WORKER_HTTP_ADDR": "127.0.0.1:1", "WORKER_ID": "worker\nid", "WORKER_POLL_INTERVAL": "50ms", "WORKER_BATCH_SIZE": "1",
		"LEASE_DURATION": "2s", "MAX_ATTEMPTS": "1", "RETRY_BACKOFF_BASE": "100ms", "RETRY_BACKOFF_MAX": "100ms", "SHUTDOWN_TIMEOUT": "1s",
		"SMTP_HOST": "127.0.0.1", "SMTP_PORT": "1", "SMTP_USERNAME": "", "SMTP_PASSWORD": "", "SMTP_FROM": "work@example.test",
		"SMTP_TLS_MODE": "plain", "SMTP_TLS_INSECURE_SKIP_VERIFY": "false", "SMTP_TIMEOUT": "1s", "LOG_LEVEL": "info",
	} {
		t.Setenv(key, value)
	}
	started := time.Now()
	err := run()
	if err == nil || !strings.Contains(err.Error(), "WORKER_ID") {
		t.Fatalf("error=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("startup rejection elapsed=%s", elapsed)
	}
}

func TestServeWorkerDrainsOnSignalAndReapsBothComponents(t *testing.T) {
	signalCtx, signal := context.WithCancel(context.Background())
	worker := newLifecycleWorker()
	admin := newLifecycleAdmin()
	result := make(chan error, 1)
	go func() { result <- serveWorker(signalCtx, time.Second, worker, admin) }()
	worker.waitStarted(t)
	admin.waitStarted(t)
	signal()
	worker.waitClaimsStopped(t)
	select {
	case err := <-result:
		t.Fatalf("serveWorker returned before the active attempt drained: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(worker.release)
	if err := receiveWorkerResult(t, result); err != nil {
		t.Fatal(err)
	}
	worker.waitExited(t)
	admin.waitExited(t)
}

func TestServeWorkerPreservesContextCanceledCompletionErrorDuringDrain(t *testing.T) {
	signalCtx, signal := context.WithCancel(context.Background())
	worker := newLifecycleWorker()
	worker.releaseError = context.Canceled
	admin := newLifecycleAdmin()
	result := make(chan error, 1)
	go func() { result <- serveWorker(signalCtx, time.Second, worker, admin) }()
	worker.waitStarted(t)
	admin.waitStarted(t)
	signal()
	worker.waitClaimsStopped(t)
	close(worker.release)
	if err := receiveWorkerResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	worker.waitExited(t)
	admin.waitExited(t)
}

func TestServeWorkerForcesCancellationAtGraceDeadlineAndReapsBothComponents(t *testing.T) {
	signalCtx, signal := context.WithCancel(context.Background())
	worker := newLifecycleWorker()
	admin := newLifecycleAdmin()
	admin.blockShutdown = true
	result := make(chan error, 1)
	started := time.Now()
	go func() { result <- serveWorker(signalCtx, 30*time.Millisecond, worker, admin) }()
	worker.waitStarted(t)
	admin.waitStarted(t)
	signal()
	err := receiveWorkerResult(t, result)
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Fatalf("elapsed=%s", elapsed)
	}
	worker.waitForceCanceled(t)
	worker.waitExited(t)
	admin.waitExited(t)
}

func TestServeWorkerJoinsGraceDeadlineWithCompletionError(t *testing.T) {
	for _, test := range []struct {
		name     string
		sentinel error
	}{
		{name: "sentinel", sentinel: errors.New("database completion failed")},
		{name: "wrapped context canceled", sentinel: fmt.Errorf("database completion failed: %w", context.Canceled)},
		{name: "context canceled", sentinel: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			completionStarted := make(chan struct{})
			queue := &completionOnCancellationQueue{completionStarted: completionStarted, completionError: test.sentinel}
			worker, err := notifications.NewWorker(queue, successfulNotificationSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)), notifications.WorkerConfig{
				WorkerID: "worker-grace-error", BatchSize: 1, LeaseDuration: time.Minute, MaxAttempts: 3,
				BackoffBase: time.Second, BackoffMax: time.Minute, PollInterval: time.Second, Random: func() float64 { return 0 },
			})
			if err != nil {
				t.Fatal(err)
			}
			signalCtx, signal := context.WithCancel(context.Background())
			admin := newLifecycleAdmin()
			result := make(chan error, 1)
			go func() { result <- serveWorker(signalCtx, 25*time.Millisecond, worker, admin) }()
			admin.waitStarted(t)
			select {
			case <-completionStarted:
			case <-time.After(time.Second):
				t.Fatal("worker did not begin completion")
			}
			signal()
			err = receiveWorkerResult(t, result)
			if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, test.sentinel) {
				t.Fatalf("error=%v want deadline and %v", err, test.sentinel)
			}
			admin.waitExited(t)
		})
	}
}

func TestServeWorkerPreservesFirstComponentError(t *testing.T) {
	sentinel := errors.New("admin listener failed")
	worker := newLifecycleWorker()
	close(worker.release)
	admin := newLifecycleAdmin()
	admin.serveError = sentinel
	admin.blockShutdown = true
	started := time.Now()
	err := serveWorker(context.Background(), time.Second, worker, admin)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("already-stopped admin waited for shutdown timeout: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("already-stopped admin cleanup elapsed=%s", elapsed)
	}
	worker.waitExited(t)
	admin.waitExited(t)
}

func TestServeWorkerForceClosesAdminAfterPromptShutdownError(t *testing.T) {
	signalCtx, signal := context.WithCancel(context.Background())
	worker := newLifecycleWorker()
	close(worker.release)
	admin := newLifecycleAdmin()
	admin.shutdownError = errors.New("shutdown failed")
	result := make(chan error, 1)
	go func() { result <- serveWorker(signalCtx, time.Second, worker, admin) }()
	worker.waitStarted(t)
	admin.waitStarted(t)
	signal()
	err := receiveWorkerResult(t, result)
	if !errors.Is(err, admin.shutdownError) {
		t.Fatalf("error=%v", err)
	}
	worker.waitExited(t)
	admin.waitExited(t)
}

type lifecycleWorker struct {
	started       chan struct{}
	claimsStopped chan struct{}
	forceCanceled chan struct{}
	release       chan struct{}
	releaseError  error
	exited        chan struct{}
}

func newLifecycleWorker() *lifecycleWorker {
	return &lifecycleWorker{
		started:       make(chan struct{}),
		claimsStopped: make(chan struct{}),
		forceCanceled: make(chan struct{}),
		release:       make(chan struct{}),
		exited:        make(chan struct{}),
	}
}

func (worker *lifecycleWorker) RunUntilStopped(claimCtx, attemptCtx context.Context) error {
	defer close(worker.exited)
	close(worker.started)
	<-claimCtx.Done()
	close(worker.claimsStopped)
	select {
	case <-worker.release:
		return worker.releaseError
	case <-attemptCtx.Done():
		close(worker.forceCanceled)
		return attemptCtx.Err()
	}
}

func (worker *lifecycleWorker) waitStarted(t *testing.T) {
	waitClosed(t, worker.started, "worker start")
}
func (worker *lifecycleWorker) waitClaimsStopped(t *testing.T) {
	waitClosed(t, worker.claimsStopped, "claim stop")
}
func (worker *lifecycleWorker) waitForceCanceled(t *testing.T) {
	waitClosed(t, worker.forceCanceled, "worker force cancellation")
}
func (worker *lifecycleWorker) waitExited(t *testing.T) { waitClosed(t, worker.exited, "worker exit") }

type lifecycleAdmin struct {
	mu            sync.Mutex
	started       chan struct{}
	stop          chan struct{}
	exited        chan struct{}
	serveError    error
	shutdownError error
	blockShutdown bool
	stopOnce      sync.Once
}

func newLifecycleAdmin() *lifecycleAdmin {
	return &lifecycleAdmin{started: make(chan struct{}), stop: make(chan struct{}), exited: make(chan struct{})}
}

func (admin *lifecycleAdmin) ListenAndServe() error {
	defer close(admin.exited)
	close(admin.started)
	admin.mu.Lock()
	err := admin.serveError
	admin.mu.Unlock()
	if err != nil {
		return err
	}
	<-admin.stop
	return http.ErrServerClosed
}

func (admin *lifecycleAdmin) Shutdown(ctx context.Context) error {
	admin.mu.Lock()
	blocked := admin.blockShutdown
	err := admin.shutdownError
	admin.mu.Unlock()
	if err != nil {
		return err
	}
	if blocked {
		<-ctx.Done()
		return ctx.Err()
	}
	admin.stopOnce.Do(func() { close(admin.stop) })
	return nil
}

func (admin *lifecycleAdmin) Close() error {
	admin.stopOnce.Do(func() { close(admin.stop) })
	return nil
}

func (admin *lifecycleAdmin) waitStarted(t *testing.T) { waitClosed(t, admin.started, "admin start") }
func (admin *lifecycleAdmin) waitExited(t *testing.T)  { waitClosed(t, admin.exited, "admin exit") }

func waitClosed(t *testing.T, channel <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func receiveWorkerResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("serveWorker did not return")
		return nil
	}
}

type completionOnCancellationQueue struct {
	claimed           bool
	completionStarted chan struct{}
	completionError   error
}

func (queue *completionOnCancellationQueue) ClaimNext(context.Context, notifications.ClaimRequest) (notifications.Delivery, bool, error) {
	if queue.claimed {
		return notifications.Delivery{}, false, nil
	}
	queue.claimed = true
	return notifications.Delivery{
		ID: "11111111-1111-4111-8111-111111111111", WorkItemID: "11111111-1111-4111-8111-111111111111",
		RecipientEmail: "alice@example.test", RecipientName: "Alice",
		Payload:   []byte(`{"work_item_id":"11111111-1111-4111-8111-111111111111","work_item_title":"Task","assignee_name":"Alice","assigned_by_name":"Maya","assigned_at":"2026-08-12T12:00:00Z"}`),
		MessageID: "<grace-error@example.test>", LeaseToken: "22222222-2222-4222-8222-222222222222", AttemptCount: 1, CycleAttemptCount: 1,
	}, true, nil
}

func (queue *completionOnCancellationQueue) Complete(ctx context.Context, _ notifications.Completion) error {
	close(queue.completionStarted)
	<-ctx.Done()
	return queue.completionError
}

type successfulNotificationSender struct{}

func (successfulNotificationSender) Send(context.Context, notifications.Message) error { return nil }
