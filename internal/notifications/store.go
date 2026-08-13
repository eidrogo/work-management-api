package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eidrogo/work-management-api/internal/platform/id"
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
COALESCE(last_error_code,''),
COALESCE(last_error,''),
message_id,
COALESCE(provider_message_id,''),
COALESCE(locked_by,''),
COALESCE(lease_token::text,''),
locked_until,
created_at,
updated_at,
last_attempt_at,
sent_at,
deduplication_key`

type ClaimRequest struct {
	WorkerID      string
	LeaseDuration time.Duration
	MaxAttempts   int
}
type Completion struct {
	DeliveryID    string
	WorkerID      string
	LeaseToken    string
	AttemptNumber int
	Outcome       Outcome
	RetryAfter    time.Duration
	FailureCode   FailureCode
}

func (store *Store) ClaimNext(ctx context.Context, request ClaimRequest) (Delivery, bool, error) {
	request.WorkerID = strings.TrimSpace(request.WorkerID)
	if request.WorkerID == "" || request.LeaseDuration <= 0 || request.MaxAttempts < 1 {
		return Delivery{}, false, fmt.Errorf("%w: invalid claim request", ErrInvalidInput)
	}
	leaseMicros := request.LeaseDuration.Microseconds()
	if leaseMicros < 1 {
		return Delivery{}, false, fmt.Errorf("%w: invalid claim request", ErrInvalidInput)
	}
	for {
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return Delivery{}, false, err
		}
		candidate, err := scanDelivery(tx.QueryRowContext(ctx, `SELECT `+notificationColumns+`
FROM notification_deliveries
WHERE (status='PENDING' AND next_attempt_at <= clock_timestamp())
   OR (status='PROCESSING' AND locked_until <= clock_timestamp())
ORDER BY CASE WHEN status='PENDING' THEN next_attempt_at ELSE locked_until END,
         created_at,
         id
FOR UPDATE SKIP LOCKED
LIMIT 1`))
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return Delivery{}, false, nil
		}
		if err != nil {
			_ = tx.Rollback()
			return Delivery{}, false, err
		}
		var dbNow time.Time
		if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			_ = tx.Rollback()
			return Delivery{}, false, err
		}
		dbNow = dbNow.UTC()
		if candidate.Status == StatusProcessing && candidate.AttemptCount > 0 {
			started := candidate.CreatedAt
			if candidate.LastAttemptAt != nil {
				started = *candidate.LastAttemptAt
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO notification_attempts(notification_id,attempt_number,worker_id,outcome,error_code,error_message,started_at,finished_at)
VALUES($1,$2,COALESCE(NULLIF($3,''),'unknown-worker'),'ABANDONED',$4,$5,$6,$7)`, candidate.ID, candidate.AttemptCount, candidate.LockedBy, string(FailureLeaseExpired), SafeFailureMessage(FailureLeaseExpired), started, dbNow); err != nil {
				_ = tx.Rollback()
				return Delivery{}, false, err
			}
		}
		if candidate.CycleAttemptCount >= request.MaxAttempts {
			if _, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='FAILED',next_attempt_at=NULL,
last_error_code=$1,last_error=$2,locked_by=NULL,lease_token=NULL,locked_until=NULL,updated_at=$3 WHERE id=$4`, string(FailureRetryLimit), SafeFailureMessage(FailureRetryLimit), dbNow, candidate.ID); err != nil {
				_ = tx.Rollback()
				return Delivery{}, false, err
			}
			if err = tx.Commit(); err != nil {
				return Delivery{}, false, err
			}
			continue
		}
		updated, scanErr := scanDelivery(tx.QueryRowContext(ctx, `UPDATE notification_deliveries
SET status='PROCESSING',attempt_count=attempt_count+1,cycle_attempt_count=cycle_attempt_count+1,
next_attempt_at=NULL,locked_by=$1,lease_token=gen_random_uuid(),locked_until=$2::timestamptz+$3*interval '1 microsecond',last_attempt_at=$2,updated_at=$2
WHERE id=$4 RETURNING `+notificationColumns, request.WorkerID, dbNow, leaseMicros, candidate.ID))
		if scanErr != nil {
			_ = tx.Rollback()
			return Delivery{}, false, scanErr
		}
		if err = tx.Commit(); err != nil {
			return Delivery{}, false, err
		}
		return updated, true, nil
	}
}

func (store *Store) Complete(ctx context.Context, completion Completion) error {
	completion.WorkerID = strings.TrimSpace(completion.WorkerID)
	if !id.IsUUID(completion.DeliveryID) || completion.WorkerID == "" || !id.IsUUID(completion.LeaseToken) || completion.AttemptNumber < 1 {
		return fmt.Errorf("%w: invalid completion", ErrInvalidInput)
	}
	switch completion.Outcome {
	case OutcomeSent:
		if completion.FailureCode != "" || completion.RetryAfter != 0 {
			return fmt.Errorf("%w: sent completion cannot include failure details", ErrInvalidInput)
		}
	case OutcomeRetryScheduled:
		if completion.RetryAfter <= 0 || !completion.FailureCode.Valid() {
			return fmt.Errorf("%w: retry requires delay and failure code", ErrInvalidInput)
		}
	case OutcomeFailed:
		if completion.RetryAfter != 0 || !completion.FailureCode.Valid() {
			return fmt.Errorf("%w: failed completion requires a failure code", ErrInvalidInput)
		}
	default:
		return fmt.Errorf("%w: invalid completion outcome", ErrInvalidInput)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var startedAt, lockedUntil time.Time
	err = tx.QueryRowContext(ctx, `SELECT last_attempt_at,locked_until FROM notification_deliveries
WHERE id=$1 AND status='PROCESSING' AND locked_by=$2 AND lease_token=$3 AND attempt_count=$4
FOR UPDATE`, completion.DeliveryID, completion.WorkerID, completion.LeaseToken, completion.AttemptNumber).Scan(&startedAt, &lockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	var dbNow time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		return err
	}
	dbNow = dbNow.UTC()
	if !lockedUntil.After(dbNow) {
		return ErrLeaseLost
	}
	var result sql.Result
	var errorCode, errorMessage any
	switch completion.Outcome {
	case OutcomeSent:
		result, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='SENT',next_attempt_at=NULL,last_error_code=NULL,last_error=NULL,
locked_by=NULL,lease_token=NULL,locked_until=NULL,sent_at=$1,updated_at=$1
WHERE id=$2 AND status='PROCESSING' AND locked_by=$3 AND lease_token=$4 AND attempt_count=$5 AND locked_until>$1`, dbNow, completion.DeliveryID, completion.WorkerID, completion.LeaseToken, completion.AttemptNumber)
	case OutcomeRetryScheduled:
		errorCode, errorMessage = string(completion.FailureCode), SafeFailureMessage(completion.FailureCode)
		retryMicros := completion.RetryAfter.Microseconds()
		if retryMicros < 1 {
			return fmt.Errorf("%w: retry delay must be at least one microsecond", ErrInvalidInput)
		}
		result, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='PENDING',next_attempt_at=$1::timestamptz+$2*interval '1 microsecond',last_error_code=$3,last_error=$4,
locked_by=NULL,lease_token=NULL,locked_until=NULL,updated_at=$1
WHERE id=$5 AND status='PROCESSING' AND locked_by=$6 AND lease_token=$7 AND attempt_count=$8 AND locked_until>$1`, dbNow, retryMicros, errorCode, errorMessage, completion.DeliveryID, completion.WorkerID, completion.LeaseToken, completion.AttemptNumber)
	case OutcomeFailed:
		errorCode, errorMessage = string(completion.FailureCode), SafeFailureMessage(completion.FailureCode)
		result, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='FAILED',next_attempt_at=NULL,last_error_code=$1,last_error=$2,
locked_by=NULL,lease_token=NULL,locked_until=NULL,updated_at=$3
WHERE id=$4 AND status='PROCESSING' AND locked_by=$5 AND lease_token=$6 AND attempt_count=$7 AND locked_until>$3`, errorCode, errorMessage, dbNow, completion.DeliveryID, completion.WorkerID, completion.LeaseToken, completion.AttemptNumber)
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
	if _, err = tx.ExecContext(ctx, `INSERT INTO notification_attempts(notification_id,attempt_number,worker_id,outcome,error_code,error_message,started_at,finished_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, completion.DeliveryID, completion.AttemptNumber, completion.WorkerID, string(completion.Outcome), errorCode, errorMessage, startedAt, dbNow); err != nil {
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
	defer func() { _ = rows.Close() }()
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
	rows, err := store.db.QueryContext(ctx, `SELECT id,attempt_number,worker_id,outcome,COALESCE(error_code,''),COALESCE(error_message,''),started_at,finished_at
FROM notification_attempts WHERE notification_id=$1 ORDER BY attempt_number ASC`, notificationID)
	if err != nil {
		return Delivery{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var attempt Attempt
		var outcome string
		if err = rows.Scan(&attempt.Sequence, &attempt.AttemptNumber, &attempt.WorkerID, &outcome, &attempt.ErrorCode, &attempt.ErrorMessage, &attempt.StartedAt, &attempt.FinishedAt); err != nil {
			return Delivery{}, err
		}
		attempt.Outcome = Outcome(outcome)
		delivery.Attempts = append(delivery.Attempts, attempt)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return Delivery{}, err
	}
	if err = rows.Close(); err != nil {
		return Delivery{}, err
	}
	replayRows, err := store.db.QueryContext(ctx, `SELECT id,delivery_cycle,actor_id,request_id,channel,reason,occurred_at
FROM notification_replay_events WHERE notification_id=$1 ORDER BY id ASC`, notificationID)
	if err != nil {
		return Delivery{}, err
	}
	defer func() { _ = replayRows.Close() }()
	for replayRows.Next() {
		var replay ReplayEvent
		if err = replayRows.Scan(&replay.Sequence, &replay.DeliveryCycle, &replay.ActorID, &replay.RequestID, &replay.Channel, &replay.Reason, &replay.OccurredAt); err != nil {
			return Delivery{}, err
		}
		delivery.Replays = append(delivery.Replays, replay)
	}
	return delivery, replayRows.Err()
}

func (store *Store) Replay(ctx context.Context, request ReplayRequest) (Delivery, error) {
	request, err := validateReplayRequest(request)
	if err != nil {
		return Delivery{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Delivery{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var cycle int
	err = tx.QueryRowContext(ctx, `SELECT status,delivery_cycle FROM notification_deliveries WHERE id=$1 FOR UPDATE`, request.NotificationID).Scan(&status, &cycle)
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, ErrResourceNotFound
	}
	if err != nil {
		return Delivery{}, err
	}
	if Status(status) != StatusFailed {
		return Delivery{}, ErrNotReplayable
	}
	if cycle >= MaxDeliveryCycles {
		return Delivery{}, ErrReplayLimitReached
	}
	var replayedAt time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&replayedAt); err != nil {
		return Delivery{}, err
	}
	replayedAt = replayedAt.UTC()
	delivery, err := scanDelivery(tx.QueryRowContext(ctx, `UPDATE notification_deliveries SET
status='PENDING',delivery_cycle=delivery_cycle+1,cycle_attempt_count=0,next_attempt_at=$1,
last_error_code=NULL,last_error=NULL,locked_by=NULL,lease_token=NULL,locked_until=NULL,updated_at=$1
WHERE id=$2 AND status='FAILED' AND delivery_cycle=$3
RETURNING `+notificationColumns, replayedAt, request.NotificationID, cycle))
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, ErrNotReplayable
	}
	if err != nil {
		return Delivery{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO notification_replay_events(
notification_id,delivery_cycle,actor_id,request_id,channel,reason,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7)`, request.NotificationID, delivery.DeliveryCycle, request.ActorID, request.RequestID, string(request.Channel), request.Reason, replayedAt); err != nil {
		return Delivery{}, err
	}
	if err = tx.Commit(); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
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
		&delivery.DeliveryCycle, &delivery.CycleAttemptCount, &nextAttempt, &delivery.LastErrorCode, &delivery.LastError,
		&delivery.MessageID, &delivery.ProviderMessageID, &delivery.LockedBy, &delivery.LeaseToken, &lockedUntil,
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
