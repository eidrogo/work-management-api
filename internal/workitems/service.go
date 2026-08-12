package workitems

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
)

type IDGenerator func() (string, error)

type Service struct {
	db    *sql.DB
	clock clock.Clock
	newID IDGenerator
}

func NewService(db *sql.DB, current clock.Clock, newID IDGenerator) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("work-item database is required")
	}
	if current == nil {
		return nil, fmt.Errorf("work-item clock is required")
	}
	if newID == nil {
		return nil, fmt.Errorf("work-item id generator is required")
	}
	return &Service{db: db, clock: current, newID: newID}, nil
}

func requestHash(input CreateInput) string {
	sum := sha256.Sum256([]byte(input.Title + "\x00" + input.Description))
	return hex.EncodeToString(sum[:])
}

func (service *Service) Create(ctx context.Context, actor auth.Principal, input CreateInput) (MutationResult, error) {
	if err := AuthorizeCreate(actor); err != nil {
		return MutationResult{}, err
	}
	input, err := ValidateCreateInput(input)
	if err != nil {
		return MutationResult{}, err
	}
	itemID, err := service.newID()
	if err != nil {
		return MutationResult{}, fmt.Errorf("generate work-item id: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return MutationResult{}, fmt.Errorf("generate event id: %w", err)
	}
	now := service.clock.Now()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult{}, fmt.Errorf("begin create transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
INSERT INTO idempotency_keys(actor_id,operation,idempotency_key,request_hash,resource_id,created_at)
VALUES($1,'CREATE_WORK_ITEM',$2,$3,$4,$5)
ON CONFLICT(actor_id,operation,idempotency_key) DO NOTHING`, actor.ID, input.IdempotencyKey, requestHash(input), itemID, now)
	if err != nil {
		return MutationResult{}, fmt.Errorf("reserve idempotency key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return MutationResult{}, err
	}
	if affected == 0 {
		var existingHash, existingID string
		if err = tx.QueryRowContext(ctx, `SELECT request_hash,resource_id::text FROM idempotency_keys
WHERE actor_id=$1 AND operation='CREATE_WORK_ITEM' AND idempotency_key=$2`, actor.ID, input.IdempotencyKey).
			Scan(&existingHash, &existingID); err != nil {
			return MutationResult{}, fmt.Errorf("read idempotency key: %w", err)
		}
		if existingHash != requestHash(input) {
			return MutationResult{}, newError(ErrIdempotencyKeyReused, "IDEMPOTENCY_KEY_REUSED", "The idempotency key was already used with a different request.", nil)
		}
		item, err := queryWorkItem(ctx, tx, existingID)
		if err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Item: item, Idempotent: true}, nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_items(id,title,description,status,creator_id,assignee_id,version,created_at,updated_at)
VALUES($1,$2,$3,'UNASSIGNED',$4,NULL,1,$5,$5)`, itemID, input.Title, input.Description, actor.ID, now); err != nil {
		return MutationResult{}, fmt.Errorf("insert work item: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_item_events(public_id,work_item_id,actor_id,event_type,from_status,to_status,occurred_at)
VALUES($1,$2,$3,'WORK_ITEM_CREATED',NULL,'UNASSIGNED',$4)`, eventID, itemID, actor.ID, now); err != nil {
		return MutationResult{}, fmt.Errorf("insert creation event: %w", err)
	}
	item, err := queryWorkItem(ctx, tx, itemID)
	if err != nil {
		return MutationResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return MutationResult{}, fmt.Errorf("commit create: %w", err)
	}
	return MutationResult{Item: item}, nil
}

func (service *Service) Get(ctx context.Context, actor auth.Principal, itemID string) (WorkItem, error) {
	if err := ValidateID(itemID); err != nil {
		return WorkItem{}, err
	}
	item, err := queryWorkItem(ctx, service.db, itemID)
	if err != nil {
		return WorkItem{}, err
	}
	if err = AuthorizeRead(actor, item); err != nil {
		return WorkItem{}, err
	}
	return item, nil
}

func (service *Service) ListManager(ctx context.Context, actor auth.Principal, input ListInput) (Page, error) {
	if err := AuthorizeManager(actor); err != nil {
		return Page{}, err
	}
	input, err := ValidateListInput(input)
	if err != nil {
		return Page{}, err
	}
	return service.list(ctx, input, "")
}
func (service *Service) ListOwn(ctx context.Context, actor auth.Principal, input ListInput) (Page, error) {
	if err := AuthorizeWorker(actor); err != nil {
		return Page{}, err
	}
	input.AssigneeID = ""
	input, err := ValidateListInput(input)
	if err != nil {
		return Page{}, err
	}
	return service.list(ctx, input, actor.ID)
}
func (service *Service) list(ctx context.Context, input ListInput, forcedAssignee string) (Page, error) {
	var where []string
	var args []any
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if input.Status != "" {
		add("work_items.status = $%d", string(input.Status))
	}
	assignee := forcedAssignee
	if assignee == "" {
		assignee = input.AssigneeID
	}
	if assignee != "" {
		add("work_items.assignee_id = $%d", assignee)
	}
	if input.Cursor != "" {
		cursor, err := DecodeListCursor(input.Cursor)
		if err != nil {
			return Page{}, err
		}
		args = append(args, cursor.CreatedAt, cursor.ID)
		where = append(where, fmt.Sprintf("(work_items.created_at, work_items.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	query := workItemSelect
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, input.Limit+1)
	query += fmt.Sprintf(" ORDER BY work_items.created_at DESC, work_items.id DESC LIMIT $%d", len(args))
	rows, err := service.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("list work items: %w", err)
	}
	defer rows.Close()
	items := make([]WorkItem, 0, input.Limit+1)
	for rows.Next() {
		item, err := scanWorkItem(rows)
		if err != nil {
			return Page{}, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return Page{}, err
	}
	page := Page{Items: items}
	if len(items) > input.Limit {
		page.Items = items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = EncodeListCursor(ListCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return Page{}, err
		}
	}
	return page, nil
}

func (service *Service) History(ctx context.Context, actor auth.Principal, itemID string, input HistoryInput) (EventPage, error) {
	if err := ValidateID(itemID); err != nil {
		return EventPage{}, err
	}
	input, err := ValidateHistoryInput(input)
	if err != nil {
		return EventPage{}, err
	}
	tx, err := service.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return EventPage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := queryWorkItem(ctx, tx, itemID)
	if err != nil {
		return EventPage{}, err
	}
	if err = AuthorizeRead(actor, item); err != nil {
		return EventPage{}, err
	}
	after := int64(0)
	if input.Cursor != "" {
		after, err = DecodeEventCursor(input.Cursor)
		if err != nil {
			return EventPage{}, err
		}
	}
	rows, err := tx.QueryContext(ctx, eventSelect+` WHERE work_item_events.work_item_id=$1 AND work_item_events.id>$2
ORDER BY work_item_events.id ASC LIMIT $3`, itemID, after, input.Limit+1)
	if err != nil {
		return EventPage{}, err
	}
	var events []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			_ = rows.Close()
			return EventPage{}, err
		}
		events = append(events, event)
	}
	if err = rows.Close(); err != nil {
		return EventPage{}, err
	}
	page := EventPage{Items: events}
	if len(events) > input.Limit {
		page.Items = events[:input.Limit]
		page.NextCursor, err = EncodeEventCursor(page.Items[len(page.Items)-1].ID)
		if err != nil {
			return EventPage{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return EventPage{}, err
	}
	return page, nil
}

func (service *Service) findUser(ctx context.Context, query queryRower, userID string) (auth.User, error) {
	var user auth.User
	var role string
	err := query.QueryRowContext(ctx, `SELECT id::text,display_name,email,role FROM users WHERE id=$1`, userID).
		Scan(&user.ID, &user.DisplayName, &user.Email, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, ErrResourceNotFound
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("load user: %w", err)
	}
	user.Role = auth.Role(role)
	return user, nil
}
