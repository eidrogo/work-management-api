package workitems

import "time"

type Status string

const (
	StatusUnassigned Status = "UNASSIGNED"
	StatusAssigned   Status = "ASSIGNED"
	StatusInProgress Status = "IN_PROGRESS"
	StatusCompleted  Status = "COMPLETED"
)

func (status Status) Valid() bool {
	switch status {
	case StatusUnassigned, StatusAssigned, StatusInProgress, StatusCompleted:
		return true
	default:
		return false
	}
}

type EventType string

const (
	EventCreated          EventType = "WORK_ITEM_CREATED"
	EventAssigned         EventType = "WORK_ITEM_ASSIGNED"
	EventReassigned       EventType = "WORK_ITEM_REASSIGNED"
	EventMarkedInProgress EventType = "WORK_ITEM_MARKED_IN_PROGRESS"
	EventCompleted        EventType = "WORK_ITEM_COMPLETED"
)

type WorkItem struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Status       Status    `json:"status"`
	CreatorID    string    `json:"creator_id"`
	CreatorName  string    `json:"creator_name"`
	AssigneeID   string    `json:"assignee_id,omitempty"`
	AssigneeName string    `json:"assignee_name,omitempty"`
	Version      int64     `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Event struct {
	ID                 int64     `json:"sequence"`
	PublicID           string    `json:"id"`
	WorkItemID         string    `json:"work_item_id"`
	ActorID            string    `json:"actor_id"`
	ActorName          string    `json:"actor_name"`
	Type               EventType `json:"type"`
	FromStatus         Status    `json:"from_status,omitempty"`
	ToStatus           Status    `json:"to_status,omitempty"`
	PreviousAssigneeID string    `json:"previous_assignee_id,omitempty"`
	NewAssigneeID      string    `json:"new_assignee_id,omitempty"`
	Reason             string    `json:"reason,omitempty"`
	OccurredAt         time.Time `json:"occurred_at"`
}

type CreateInput struct {
	Title, Description, IdempotencyKey string
}
type AssignmentInput struct {
	AssigneeID, Reason string
	ExpectedVersion    int64
}
type TransitionInput struct{ ExpectedVersion int64 }
type ListInput struct {
	Status     Status
	AssigneeID string
	Limit      int
	Cursor     string
}
type HistoryInput struct {
	Limit  int
	Cursor string
}
type Page struct {
	Items      []WorkItem `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}
type EventPage struct {
	Items      []Event `json:"items"`
	NextCursor string  `json:"next_cursor,omitempty"`
}
type MutationResult struct {
	Item       WorkItem
	Idempotent bool
}
type AssignmentDecision struct {
	Noop      bool
	EventType EventType
}
type TransitionDecision struct {
	Noop      bool
	EventType EventType
}

type AssignmentEmailPayload struct {
	WorkItemID     string `json:"work_item_id"`
	WorkItemTitle  string `json:"work_item_title"`
	AssigneeName   string `json:"assignee_name"`
	AssignedByName string `json:"assigned_by_name"`
	Reason         string `json:"reason,omitempty"`
	AssignedAt     string `json:"assigned_at"`
}
