package notifications

import (
	"context"
	"encoding/json"
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
	if queue == nil || sender == nil || logger == nil || strings.TrimSpace(config.WorkerID) == "" || config.BatchSize < 1 || config.BatchSize > 100 || config.LeaseDuration <= 0 || config.MaxAttempts < 1 || config.BackoffBase <= 0 || config.BackoffMax < config.BackoffBase || config.PollInterval <= 0 || config.Random == nil {
		return nil, fmt.Errorf("invalid notification worker configuration")
	}
	return &Worker{queue: queue, sender: sender, logger: logger, config: config}, nil
}
func (worker *Worker) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			processed, err := worker.ProcessBatch(ctx)
			if err != nil && ctx.Err() == nil {
				worker.logger.Error("notification batch failed", "error", err)
			}
			delay := worker.config.PollInterval
			if processed >= worker.config.BatchSize {
				delay = 0
			}
			timer.Reset(delay)
		}
	}
}
func (worker *Worker) ProcessBatch(ctx context.Context) (int, error) {
	processed := 0
	for processed < worker.config.BatchSize {
		delivery, found, err := worker.queue.ClaimNext(ctx, ClaimRequest{WorkerID: worker.config.WorkerID, LeaseDuration: worker.config.LeaseDuration, MaxAttempts: worker.config.MaxAttempts})
		if err != nil {
			return processed, err
		}
		if !found {
			break
		}
		message, renderErr := renderAssignmentMessage(delivery)
		sendErr := renderErr
		if sendErr == nil {
			sendErr = worker.sender.Send(ctx, message)
		}
		if ctx.Err() != nil {
			return processed, ctx.Err()
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
		if err = worker.queue.Complete(ctx, completion); err != nil {
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
	return Message{To: delivery.RecipientEmail, ToName: delivery.RecipientName, Subject: "New work assigned: " + payload.WorkItemTitle, TextBody: body.String(), MessageID: delivery.MessageID}, nil
}
