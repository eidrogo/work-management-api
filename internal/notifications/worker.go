package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/eidrogo/work-management-api/internal/workitems"
)

type Queue interface {
	ClaimNext(context.Context, ClaimRequest) (Delivery, bool, error)
	Complete(context.Context, Completion) error
}
type AttemptMetricRecorder interface{ RecordNotificationAttempt(string) }
type WorkerConfig struct {
	WorkerID      string
	BatchSize     int
	LeaseDuration time.Duration
	MaxAttempts   int
	BackoffBase   time.Duration
	BackoffMax    time.Duration
	PollInterval  time.Duration
	Random        func() float64
	Metrics       AttemptMetricRecorder
}
type Worker struct {
	queue  Queue
	sender Sender
	logger *slog.Logger
	config WorkerConfig
}

func NewWorker(queue Queue, sender Sender, logger *slog.Logger, config WorkerConfig) (*Worker, error) {
	workerID, workerIDErr := ValidateWorkerID(config.WorkerID)
	if queue == nil || sender == nil || logger == nil || workerIDErr != nil || config.BatchSize < 1 || config.BatchSize > 100 || config.LeaseDuration <= 0 || config.MaxAttempts < 1 || config.BackoffBase <= 0 || config.BackoffMax < config.BackoffBase || config.PollInterval <= 0 || config.Random == nil {
		return nil, fmt.Errorf("invalid notification worker configuration")
	}
	config.WorkerID = workerID
	return &Worker{queue: queue, sender: sender, logger: logger, config: config}, nil
}
func (worker *Worker) Run(ctx context.Context) error {
	return worker.RunUntilStopped(ctx, ctx)
}
func (worker *Worker) RunUntilStopped(claimCtx, attemptCtx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			processed, err := worker.processBatch(claimCtx, attemptCtx)
			if attemptCtx.Err() != nil {
				return attemptCtx.Err()
			}
			if err != nil {
				if claimCtx.Err() != nil {
					return err
				}
				worker.logger.Error("notification batch failed", "error", err)
			}
			if claimCtx.Err() != nil {
				return nil
			}
			delay := worker.config.PollInterval
			if processed >= worker.config.BatchSize {
				delay = 0
			}
			timer.Reset(delay)
		case <-claimCtx.Done():
			return nil
		}
	}
}
func (worker *Worker) ProcessBatch(ctx context.Context) (int, error) {
	return worker.processBatch(ctx, ctx)
}
func (worker *Worker) processBatch(claimCtx, attemptCtx context.Context) (int, error) {
	processed := 0
	for processed < worker.config.BatchSize {
		select {
		case <-claimCtx.Done():
			return processed, nil
		default:
		}
		delivery, found, err := worker.queue.ClaimNext(claimCtx, ClaimRequest{WorkerID: worker.config.WorkerID, LeaseDuration: worker.config.LeaseDuration, MaxAttempts: worker.config.MaxAttempts})
		if err != nil {
			if claimCtx.Err() != nil && errors.Is(err, claimCtx.Err()) {
				return processed, nil
			}
			return processed, err
		}
		if !found {
			break
		}
		message, renderErr := renderAssignmentMessage(delivery)
		sendErr := renderErr
		if sendErr == nil {
			sendErr = worker.sender.Send(attemptCtx, message)
		}
		if attemptCtx.Err() != nil {
			return processed, attemptCtx.Err()
		}
		completion := Completion{DeliveryID: delivery.ID, WorkerID: worker.config.WorkerID, LeaseToken: delivery.LeaseToken, AttemptNumber: delivery.AttemptCount}
		switch {
		case sendErr == nil:
			completion.Outcome = OutcomeSent
		case !IsRetryable(sendErr) || delivery.CycleAttemptCount >= worker.config.MaxAttempts:
			completion.Outcome = OutcomeFailed
			completion.FailureCode = FailureCodeOf(sendErr)
		default:
			completion.Outcome = OutcomeRetryScheduled
			completion.FailureCode = FailureCodeOf(sendErr)
			completion.RetryAfter = RetryDelay(worker.config.BackoffBase, worker.config.BackoffMax, delivery.CycleAttemptCount, worker.config.Random())
		}
		if err = worker.queue.Complete(attemptCtx, completion); err != nil {
			return processed, err
		}
		processed++
		if worker.config.Metrics != nil {
			worker.config.Metrics.RecordNotificationAttempt(string(completion.Outcome))
		}
		worker.logger.Info("notification attempt completed", "notification_id", delivery.ID, "work_item_id", delivery.WorkItemID, "attempt", delivery.AttemptCount, "outcome", completion.Outcome)
	}
	return processed, nil
}
func renderAssignmentMessage(delivery Delivery) (Message, error) {
	var payload workitems.AssignmentEmailPayload
	if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
		return Message{}, Permanent(FailureNotificationPayloadInvalid, fmt.Errorf("decode assignment payload: %w", err))
	}
	if payload.WorkItemID == "" || strings.TrimSpace(payload.WorkItemTitle) == "" {
		return Message{}, Permanent(FailureNotificationPayloadInvalid, fmt.Errorf("assignment payload is incomplete"))
	}
	assignee := strings.TrimSpace(payload.AssigneeName)
	if assignee == "" {
		assignee = delivery.RecipientName
	}
	manager := strings.TrimSpace(payload.AssignedByName)
	if manager == "" {
		manager = "A manager"
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Hello %s,\n\n%s assigned you a new work item.\n\nTitle: %s\nWork item ID: %s\n", assignee, manager, payload.WorkItemTitle, payload.WorkItemID)
	if strings.TrimSpace(payload.Reason) != "" {
		fmt.Fprintf(&body, "Assignment reason: %s\n", payload.Reason)
	}
	body.WriteString("\nOpen your work queue to review and start the item.\n")
	return Message{To: delivery.RecipientEmail, ToName: delivery.RecipientName, Subject: assignmentSubject(payload.WorkItemTitle), TextBody: body.String(), MessageID: delivery.MessageID}, nil
}

func assignmentSubject(title string) string {
	const prefix = "New work assigned: "
	const maximumSubjectRunes = 60
	runes := []rune(title)
	if len(runes) > maximumSubjectRunes {
		return prefix + string(runes[:maximumSubjectRunes]) + "…"
	}
	return prefix + title
}
