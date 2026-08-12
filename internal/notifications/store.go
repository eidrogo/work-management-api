package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("notification database is required")
	}
	return &Store{db: db}, nil
}

const notificationColumns = `
id::text,
work_item_id::text,
assignment_event_id,
recipient_email,
recipient_name,
event_type,
payload,
status,
attempt_count,
delivery_cycle,
cycle_attempt_count,
next_attempt_at,
COALESCE(last_error,''),
message_id,
COALESCE(provider_message_id,''),
COALESCE(locked_by,''),
locked_until,
created_at,
updated_at,
last_attempt_at,
sent_at,
deduplication_key`

type ClaimRequest struct {
	Now           time.Time
	WorkerID      string
	BatchSize     int
	LeaseDuration time.Duration
	MaxAttempts   int
}
type Completion struct {
	Delivery      Delivery
	WorkerID      string
	FinishedAt    time.Time
	Outcome       Outcome
	NextAttemptAt *time.Time
	ErrorMessage  string
}

func (store *Store) ClaimBatch(ctx context.Context, request ClaimRequest) ([]Delivery, error) {
	if request.WorkerID == "" || request.BatchSize < 1 || request.BatchSize > 100 || request.LeaseDuration <= 0 || request.MaxAttempts < 1 || request.Now.IsZero() {
		return nil, fmt.Errorf("%w: invalid claim request", ErrInvalidInput)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT `+notificationColumns+`
FROM notification_deliveries
WHERE (status='PENDING' AND next_attempt_at <= $1)
   OR (status='PROCESSING' AND locked_until <= $1)
ORDER BY COALESCE(next_attempt_at,locked_until),created_at,id
FOR UPDATE SKIP LOCKED
LIMIT $2`, request.Now, request.BatchSize)
	if err != nil {
		return nil, err
	}
	var candidates []Delivery
	for rows.Next() {
		delivery, scanErr := scanDelivery(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		candidates = append(candidates, delivery)
	}
	_ = rows.Close()
	var claimed []Delivery
	for _, candidate := range candidates {
		if candidate.Status == StatusProcessing && candidate.AttemptCount > 0 {
			started := candidate.CreatedAt
			if candidate.LastAttemptAt != nil {
				started = *candidate.LastAttemptAt
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO notification_attempts(notification_id,attempt_number,worker_id,outcome,error_message,started_at,finished_at)
VALUES($1,$2,COALESCE(NULLIF($3,''),'unknown-worker'),'ABANDONED','worker lease expired',$4,$5)
ON CONFLICT(notification_id,attempt_number) DO NOTHING`, candidate.ID, candidate.AttemptCount, candidate.LockedBy, started, request.Now); err != nil {
				return nil, err
			}
		}
		if candidate.CycleAttemptCount >= request.MaxAttempts {
			if _, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='FAILED',next_attempt_at=NULL,
last_error='delivery lease expired after retry budget was exhausted',locked_by=NULL,locked_until=NULL,updated_at=$1 WHERE id=$2`, request.Now, candidate.ID); err != nil {
				return nil, err
			}
			continue
		}
		updated, scanErr := scanDelivery(tx.QueryRowContext(ctx, `UPDATE notification_deliveries
SET status='PROCESSING',attempt_count=attempt_count+1,cycle_attempt_count=cycle_attempt_count+1,
next_attempt_at=$1,locked_by=$2,locked_until=$3,last_attempt_at=$1,updated_at=$1
WHERE id=$4 RETURNING `+notificationColumns, request.Now, request.WorkerID, request.Now.Add(request.LeaseDuration), candidate.ID))
		if scanErr != nil {
			return nil, scanErr
		}
		claimed = append(claimed, updated)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (store *Store) Complete(ctx context.Context, completion Completion) error {
	if completion.Delivery.ID == "" || completion.WorkerID == "" || completion.FinishedAt.IsZero() {
		return fmt.Errorf("%w: invalid completion", ErrInvalidInput)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var result sql.Result
	switch completion.Outcome {
	case OutcomeSent:
		result, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='SENT',next_attempt_at=NULL,last_error=NULL,
locked_by=NULL,locked_until=NULL,sent_at=$1,updated_at=$1 WHERE id=$2 AND status='PROCESSING' AND locked_by=$3 AND attempt_count=$4`, completion.FinishedAt, completion.Delivery.ID, completion.WorkerID, completion.Delivery.AttemptCount)
	case OutcomeRetryScheduled:
		if completion.NextAttemptAt == nil || !completion.NextAttemptAt.After(completion.FinishedAt) {
			return fmt.Errorf("%w: retry requires a future next attempt", ErrInvalidInput)
		}
		result, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='PENDING',next_attempt_at=$1,last_error=$2,
locked_by=NULL,locked_until=NULL,updated_at=$3 WHERE id=$4 AND status='PROCESSING' AND locked_by=$5 AND attempt_count=$6`, *completion.NextAttemptAt, boundedText(completion.ErrorMessage), completion.FinishedAt, completion.Delivery.ID, completion.WorkerID, completion.Delivery.AttemptCount)
	case OutcomeFailed:
		result, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='FAILED',next_attempt_at=NULL,last_error=$1,
locked_by=NULL,locked_until=NULL,updated_at=$2 WHERE id=$3 AND status='PROCESSING' AND locked_by=$4 AND attempt_count=$5`, boundedText(completion.ErrorMessage), completion.FinishedAt, completion.Delivery.ID, completion.WorkerID, completion.Delivery.AttemptCount)
	default:
		return fmt.Errorf("%w: invalid completion outcome", ErrInvalidInput)
	}
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	started := completion.FinishedAt
	if completion.Delivery.LastAttemptAt != nil {
		started = *completion.Delivery.LastAttemptAt
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO notification_attempts(notification_id,attempt_number,worker_id,outcome,error_message,started_at,finished_at)
VALUES($1,$2,$3,$4,$5,$6,$7)`, completion.Delivery.ID, completion.Delivery.AttemptCount, completion.WorkerID, string(completion.Outcome), nullableText(completion.ErrorMessage), started, completion.FinishedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) List(ctx context.Context, input ListInput) (Page, error) {
	var conditions []string
	var args []any
	add := func(template string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(template, len(args)))
	}
	if input.Status != "" {
		add("status=$%d", string(input.Status))
	}
	if input.WorkItemID != "" {
		add("work_item_id=$%d", input.WorkItemID)
	}
	if input.Cursor != "" {
		cursor, err := DecodeListCursor(input.Cursor)
		if err != nil {
			return Page{}, err
		}
		args = append(args, cursor.CreatedAt, cursor.ID)
		conditions = append(conditions, fmt.Sprintf("(created_at,id)<($%d,$%d)", len(args)-1, len(args)))
	}
	query := "SELECT " + notificationColumns + " FROM notification_deliveries"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, input.Limit+1)
	query += fmt.Sprintf(" ORDER BY created_at DESC,id DESC LIMIT $%d", len(args))
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var deliveries []Delivery
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return Page{}, err
		}
		deliveries = append(deliveries, delivery)
	}
	page := Page{Items: deliveries}
	if len(deliveries) > input.Limit {
		page.Items = deliveries[:input.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = EncodeListCursor(ListCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return Page{}, err
		}
	}
	return page, rows.Err()
}

func (store *Store) Get(ctx context.Context, notificationID string) (Delivery, error) {
	delivery, err := scanDelivery(store.db.QueryRowContext(ctx, "SELECT "+notificationColumns+" FROM notification_deliveries WHERE id=$1", notificationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, ErrResourceNotFound
	}
	if err != nil {
		return Delivery{}, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id,attempt_number,worker_id,outcome,COALESCE(error_message,''),started_at,finished_at
FROM notification_attempts WHERE notification_id=$1 ORDER BY attempt_number ASC`, notificationID)
	if err != nil {
		return Delivery{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var attempt Attempt
		var outcome string
		if err = rows.Scan(&attempt.Sequence, &attempt.AttemptNumber, &attempt.WorkerID, &outcome, &attempt.ErrorMessage, &attempt.StartedAt, &attempt.FinishedAt); err != nil {
			return Delivery{}, err
		}
		attempt.Outcome = Outcome(outcome)
		delivery.Attempts = append(delivery.Attempts, attempt)
	}
	return delivery, rows.Err()
}

func (store *Store) Replay(ctx context.Context, notificationID string, now time.Time) (Delivery, error) {
	delivery, err := scanDelivery(store.db.QueryRowContext(ctx, `UPDATE notification_deliveries SET status='PENDING',
delivery_cycle=delivery_cycle+1,cycle_attempt_count=0,next_attempt_at=$1,last_error=NULL,locked_by=NULL,locked_until=NULL,updated_at=$1
WHERE id=$2 AND status='FAILED' RETURNING `+notificationColumns, now, notificationID))
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := store.Get(ctx, notificationID); errors.Is(getErr, ErrResourceNotFound) {
			return Delivery{}, ErrResourceNotFound
		}
		return Delivery{}, ErrNotReplayable
	}
	return delivery, err
}

type Backlog struct {
	Pending, Processing, Failed int64
	OldestPendingAt             *time.Time
}

func (store *Store) Backlog(ctx context.Context) (Backlog, error) {
	var result Backlog
	var oldest sql.NullTime
	err := store.db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE status='PENDING'),count(*) FILTER (WHERE status='PROCESSING'),
count(*) FILTER (WHERE status='FAILED'),min(created_at) FILTER (WHERE status='PENDING') FROM notification_deliveries`).
		Scan(&result.Pending, &result.Processing, &result.Failed, &oldest)
	if err != nil {
		return Backlog{}, err
	}
	if oldest.Valid {
		value := oldest.Time.UTC()
		result.OldestPendingAt = &value
	}
	return result, nil
}

func scanDelivery(row interface{ Scan(...any) error }) (Delivery, error) {
	var delivery Delivery
	var status string
	var payload []byte
	var nextAttempt, lockedUntil, lastAttempt, sentAt sql.NullTime
	err := row.Scan(&delivery.ID, &delivery.WorkItemID, &delivery.AssignmentEventID, &delivery.RecipientEmail,
		&delivery.RecipientName, &delivery.EventType, &payload, &status, &delivery.AttemptCount,
		&delivery.DeliveryCycle, &delivery.CycleAttemptCount, &nextAttempt, &delivery.LastError,
		&delivery.MessageID, &delivery.ProviderMessageID, &delivery.LockedBy, &lockedUntil,
		&delivery.CreatedAt, &delivery.UpdatedAt, &lastAttempt, &sentAt, &delivery.DeduplicationKey)
	if err != nil {
		return Delivery{}, err
	}
	delivery.Payload = append([]byte(nil), payload...)
	delivery.Status = Status(status)
	if nextAttempt.Valid {
		value := nextAttempt.Time.UTC()
		delivery.NextAttemptAt = &value
	}
	if lockedUntil.Valid {
		value := lockedUntil.Time.UTC()
		delivery.LockedUntil = &value
	}
	if lastAttempt.Valid {
		value := lastAttempt.Time.UTC()
		delivery.LastAttemptAt = &value
	}
	if sentAt.Valid {
		value := sentAt.Time.UTC()
		delivery.SentAt = &value
	}
	return delivery, nil
}
func boundedText(value string) string {
	if len(value) > 2000 {
		return value[:2000]
	}
	return value
}
func nullableText(value string) any {
	value = boundedText(value)
	if value == "" {
		return nil
	}
	return value
}
