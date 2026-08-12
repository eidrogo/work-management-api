package workitems

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type scanner interface{ Scan(...any) error }
type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const workItemSelect = `
SELECT
    work_items.id::text,
    work_items.title,
    work_items.description,
    work_items.status,
    work_items.creator_id::text,
    creator.display_name,
    COALESCE(work_items.assignee_id::text,''),
    COALESCE(assignee.display_name,''),
    work_items.version,
    work_items.created_at,
    work_items.updated_at
FROM work_items
JOIN users AS creator ON creator.id=work_items.creator_id
LEFT JOIN users AS assignee ON assignee.id=work_items.assignee_id`

func queryWorkItem(ctx context.Context, query queryRower, itemID string) (WorkItem, error) {
	item, err := scanWorkItem(query.QueryRowContext(ctx, workItemSelect+" WHERE work_items.id=$1", itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkItem{}, ErrResourceNotFound
	}
	if err != nil {
		return WorkItem{}, fmt.Errorf("load work item: %w", err)
	}
	return item, nil
}

func scanWorkItem(row scanner) (WorkItem, error) {
	var item WorkItem
	var status string
	err := row.Scan(&item.ID, &item.Title, &item.Description, &status, &item.CreatorID, &item.CreatorName,
		&item.AssigneeID, &item.AssigneeName, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	item.Status = Status(status)
	return item, err
}

const eventSelect = `
SELECT
    work_item_events.id,
    work_item_events.public_id::text,
    work_item_events.work_item_id::text,
    work_item_events.actor_id::text,
    actor.display_name,
    work_item_events.event_type,
    COALESCE(work_item_events.from_status,''),
    COALESCE(work_item_events.to_status,''),
    COALESCE(work_item_events.previous_assignee_id::text,''),
    COALESCE(work_item_events.new_assignee_id::text,''),
    COALESCE(work_item_events.reason,''),
    work_item_events.occurred_at
FROM work_item_events
JOIN users AS actor ON actor.id=work_item_events.actor_id`

func scanEvent(row scanner) (Event, error) {
	var event Event
	var eventType, fromStatus, toStatus string
	err := row.Scan(&event.ID, &event.PublicID, &event.WorkItemID, &event.ActorID, &event.ActorName,
		&eventType, &fromStatus, &toStatus, &event.PreviousAssigneeID, &event.NewAssigneeID,
		&event.Reason, &event.OccurredAt)
	event.Type = EventType(eventType)
	event.FromStatus = Status(fromStatus)
	event.ToStatus = Status(toStatus)
	return event, err
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
