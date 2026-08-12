package workitems

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/platform/id"
)

func AuthorizeCreate(actor auth.Principal) error {
	if actor.Role != auth.RoleManager {
		return ErrPermissionDenied
	}
	return nil
}
func AuthorizeManager(actor auth.Principal) error {
	if actor.Role != auth.RoleManager {
		return ErrPermissionDenied
	}
	return nil
}
func AuthorizeWorker(actor auth.Principal) error {
	if actor.Role != auth.RoleWorker {
		return ErrPermissionDenied
	}
	return nil
}
func AuthorizeRead(actor auth.Principal, item WorkItem) error {
	if actor.Role == auth.RoleManager {
		return nil
	}
	if actor.Role != auth.RoleWorker || item.AssigneeID == "" || item.AssigneeID != actor.ID {
		return ErrResourceNotFound
	}
	return nil
}

func ValidateCreateInput(input CreateInput) (CreateInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		return CreateInput{}, newError(ErrIdempotencyKeyRequired, "IDEMPOTENCY_KEY_REQUIRED", "An Idempotency-Key header is required.", nil)
	}
	if utf8.RuneCountInString(input.IdempotencyKey) > 200 {
		return CreateInput{}, newError(ErrInvalidInput, "INVALID_INPUT", "The idempotency key is too long.", map[string]any{"field": "Idempotency-Key", "maximum": 200})
	}
	if input.Title == "" || utf8.RuneCountInString(input.Title) > 200 {
		return CreateInput{}, newError(ErrInvalidInput, "INVALID_INPUT", "Title must contain between 1 and 200 characters.", map[string]any{"field": "title", "maximum": 200})
	}
	if utf8.RuneCountInString(input.Description) > 5000 {
		return CreateInput{}, newError(ErrInvalidInput, "INVALID_INPUT", "Description must not exceed 5000 characters.", map[string]any{"field": "description", "maximum": 5000})
	}
	return input, nil
}

func ValidateAssignmentInput(input AssignmentInput) (AssignmentInput, error) {
	input.AssigneeID = strings.TrimSpace(input.AssigneeID)
	input.Reason = strings.TrimSpace(input.Reason)
	if !id.IsUUID(input.AssigneeID) {
		return AssignmentInput{}, newError(ErrInvalidInput, "INVALID_INPUT", "assignee_id must be a UUID.", map[string]any{"field": "assignee_id"})
	}
	if utf8.RuneCountInString(input.Reason) > 1000 {
		return AssignmentInput{}, newError(ErrInvalidInput, "INVALID_INPUT", "reason must not exceed 1000 characters.", map[string]any{"field": "reason", "maximum": 1000})
	}
	return input, nil
}

func ValidateAssignment(actor auth.Principal, item WorkItem, assignee auth.User, reason string) (AssignmentDecision, error) {
	if actor.Role != auth.RoleManager {
		return AssignmentDecision{}, ErrPermissionDenied
	}
	if assignee.Role != auth.RoleWorker {
		return AssignmentDecision{}, newError(ErrAssigneeMustBeWorker, "ASSIGNEE_MUST_BE_WORKER", "Only a worker can be assigned a work item.", map[string]any{"assignee_id": assignee.ID})
	}
	if item.Status == StatusCompleted {
		return AssignmentDecision{}, newError(ErrCompletedImmutable, "WORK_ITEM_COMPLETED_IMMUTABLE", "Completed work cannot be changed.", map[string]any{"current_status": item.Status})
	}
	if item.Status != StatusUnassigned && item.Status != StatusAssigned {
		return AssignmentDecision{}, newError(ErrAssignmentConflict, "ASSIGNMENT_CONFLICT", "Work can be assigned or reassigned only before it starts.", map[string]any{"current_status": item.Status})
	}
	if item.Status == StatusAssigned && item.AssigneeID == assignee.ID {
		return AssignmentDecision{Noop: true}, nil
	}
	if item.Status == StatusAssigned {
		if strings.TrimSpace(reason) == "" {
			return AssignmentDecision{}, newError(ErrReassignmentReasonRequired, "REASSIGNMENT_REASON_REQUIRED", "A reason is required when the assignee changes.", nil)
		}
		return AssignmentDecision{EventType: EventReassigned}, nil
	}
	return AssignmentDecision{EventType: EventAssigned}, nil
}

func ValidateTransition(actor auth.Principal, item WorkItem, target Status) (TransitionDecision, error) {
	if actor.Role != auth.RoleWorker {
		return TransitionDecision{}, ErrPermissionDenied
	}
	if item.AssigneeID == "" || item.AssigneeID != actor.ID {
		return TransitionDecision{}, ErrResourceNotFound
	}
	if item.Status == target && (target == StatusInProgress || target == StatusCompleted) {
		return TransitionDecision{Noop: true}, nil
	}
	if item.Status == StatusCompleted {
		return TransitionDecision{}, newError(ErrCompletedImmutable, "WORK_ITEM_COMPLETED_IMMUTABLE", "Completed work cannot be changed.", map[string]any{"current_status": item.Status, "requested_status": target})
	}
	switch {
	case item.Status == StatusAssigned && target == StatusInProgress:
		return TransitionDecision{EventType: EventMarkedInProgress}, nil
	case item.Status == StatusInProgress && target == StatusCompleted:
		return TransitionDecision{EventType: EventCompleted}, nil
	default:
		return TransitionDecision{}, newError(ErrInvalidTransition, "WORK_ITEM_INVALID_TRANSITION", fmt.Sprintf("The work item cannot move from %s to %s.", item.Status, target), map[string]any{"current_status": item.Status, "requested_status": target})
	}
}

func ValidateExpectedVersion(version int64) error {
	if version <= 0 {
		return newError(ErrExpectedVersionRequired, "EXPECTED_VERSION_REQUIRED", "A positive expected version is required.", nil)
	}
	return nil
}
func ValidateListInput(input ListInput) (ListInput, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 100 {
		return ListInput{}, newError(ErrInvalidInput, "INVALID_INPUT", "limit must be between 1 and 100.", map[string]any{"field": "limit", "maximum": 100})
	}
	if input.Status != "" && !input.Status.Valid() {
		return ListInput{}, newError(ErrInvalidInput, "INVALID_INPUT", "status is invalid.", map[string]any{"field": "status"})
	}
	if input.AssigneeID != "" && !id.IsUUID(input.AssigneeID) {
		return ListInput{}, newError(ErrInvalidInput, "INVALID_INPUT", "assignee_id must be a UUID.", map[string]any{"field": "assignee_id"})
	}
	return input, nil
}
func ValidateHistoryInput(input HistoryInput) (HistoryInput, error) {
	if input.Limit == 0 {
		input.Limit = 50
	}
	if input.Limit < 1 || input.Limit > 100 {
		return HistoryInput{}, newError(ErrInvalidInput, "INVALID_INPUT", "limit must be between 1 and 100.", map[string]any{"field": "limit", "maximum": 100})
	}
	return input, nil
}
func ValidateID(value string) error {
	if !id.IsUUID(strings.TrimSpace(value)) {
		return newError(ErrInvalidInput, "INVALID_INPUT", "resource id must be a UUID.", map[string]any{"field": "id"})
	}
	return nil
}
