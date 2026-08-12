package workitems

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/eidrogo/work-management-api/internal/auth"
)

func (service *Service) Assign(ctx context.Context, actor auth.Principal, itemID string, input AssignmentInput) (MutationResult, error) {
	if err := AuthorizeManager(actor); err != nil {
		return MutationResult{}, err
	}
	if err := ValidateID(itemID); err != nil {
		return MutationResult{}, err
	}
	input, err := ValidateAssignmentInput(input)
	if err != nil {
		return MutationResult{}, err
	}
	notificationID, err := service.newID()
	if err != nil {
		return MutationResult{}, err
	}
	eventPublicID, err := service.newID()
	if err != nil {
		return MutationResult{}, err
	}
	now := service.clock.Now()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := queryWorkItem(ctx, tx, itemID)
	if err != nil {
		return MutationResult{}, err
	}
	assignee, err := service.findUser(ctx, tx, input.AssigneeID)
	if errors.Is(err, ErrResourceNotFound) {
		return MutationResult{}, newError(ErrAssigneeMustBeWorker, "ASSIGNEE_MUST_BE_WORKER", "The assignee must reference an existing worker.", map[string]any{"assignee_id": input.AssigneeID})
	}
	if err != nil {
		return MutationResult{}, err
	}
	decision, err := ValidateAssignment(actor, item, assignee, input.Reason)
	if err != nil {
		return MutationResult{}, err
	}
	if decision.Noop {
		return MutationResult{Item: item, Idempotent: true}, nil
	}
	if err = ValidateExpectedVersion(input.ExpectedVersion); err != nil {
		return MutationResult{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE work_items SET assignee_id=$1,status='ASSIGNED',version=version+1,updated_at=$2
WHERE id=$3 AND version=$4`, assignee.ID, now, item.ID, input.ExpectedVersion)
	if err != nil {
		return MutationResult{}, fmt.Errorf("update assignment: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return MutationResult{}, err
	}
	if affected != 1 {
		current, currentErr := queryWorkItem(ctx, tx, itemID)
		if currentErr == nil && current.Status == StatusAssigned && current.AssigneeID == assignee.ID {
			return MutationResult{Item: current, Idempotent: true}, nil
		}
		return MutationResult{}, newError(ErrConcurrentModification, "CONCURRENT_MODIFICATION", "The work item changed before the assignment was committed.", map[string]any{"expected_version": input.ExpectedVersion})
	}
	var eventSequence int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO work_item_events(public_id,work_item_id,actor_id,event_type,from_status,to_status,previous_assignee_id,new_assignee_id,reason,occurred_at)
VALUES($1,$2,$3,$4,$5,'ASSIGNED',$6,$7,$8,$9) RETURNING id`, eventPublicID, item.ID, actor.ID, string(decision.EventType), string(item.Status), nullable(item.AssigneeID), assignee.ID, nullable(input.Reason), now).
		Scan(&eventSequence); err != nil {
		return MutationResult{}, fmt.Errorf("insert assignment event: %w", err)
	}
	payload, err := json.Marshal(AssignmentEmailPayload{WorkItemID: item.ID, WorkItemTitle: item.Title, AssigneeName: assignee.DisplayName, AssignedByName: actor.DisplayName, Reason: input.Reason, AssignedAt: now.Format("2006-01-02T15:04:05.999999999Z07:00")})
	if err != nil {
		return MutationResult{}, err
	}
	messageID := fmt.Sprintf("<%s@work-management.invalid>", notificationID)
	if _, err = tx.ExecContext(ctx, `INSERT INTO notification_deliveries(
 id,work_item_id,assignment_event_id,recipient_email,recipient_name,event_type,payload,status,attempt_count,delivery_cycle,cycle_attempt_count,next_attempt_at,message_id,created_at,updated_at,deduplication_key)
VALUES($1,$2,$3,$4,$5,'WORK_ITEM_ASSIGNED_EMAIL',$6::jsonb,'PENDING',0,1,0,$7,$8,$7,$7,$9)`, notificationID, item.ID, eventSequence, assignee.Email, assignee.DisplayName, string(payload), now, messageID, "work-item-assignment:"+eventPublicID); err != nil {
		return MutationResult{}, fmt.Errorf("enqueue assignment email: %w", err)
	}
	updated, err := queryWorkItem(ctx, tx, itemID)
	if err != nil {
		return MutationResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return MutationResult{}, err
	}
	return MutationResult{Item: updated}, nil
}

func (service *Service) Start(ctx context.Context, actor auth.Principal, itemID string, input TransitionInput) (MutationResult, error) {
	return service.transition(ctx, actor, itemID, StatusInProgress, input)
}
func (service *Service) Complete(ctx context.Context, actor auth.Principal, itemID string, input TransitionInput) (MutationResult, error) {
	return service.transition(ctx, actor, itemID, StatusCompleted, input)
}
func (service *Service) transition(ctx context.Context, actor auth.Principal, itemID string, target Status, input TransitionInput) (MutationResult, error) {
	if err := ValidateID(itemID); err != nil {
		return MutationResult{}, err
	}
	eventPublicID, err := service.newID()
	if err != nil {
		return MutationResult{}, err
	}
	now := service.clock.Now()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := queryWorkItem(ctx, tx, itemID)
	if err != nil {
		return MutationResult{}, err
	}
	decision, err := ValidateTransition(actor, item, target)
	if err != nil {
		return MutationResult{}, err
	}
	if decision.Noop {
		return MutationResult{Item: item, Idempotent: true}, nil
	}
	if err = ValidateExpectedVersion(input.ExpectedVersion); err != nil {
		return MutationResult{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE work_items SET status=$1,version=version+1,updated_at=$2 WHERE id=$3 AND version=$4`, string(target), now, itemID, input.ExpectedVersion)
	if err != nil {
		return MutationResult{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return MutationResult{}, err
	}
	if affected != 1 {
		current, currentErr := queryWorkItem(ctx, tx, itemID)
		if currentErr == nil && current.Status == target && current.AssigneeID == actor.ID {
			return MutationResult{Item: current, Idempotent: true}, nil
		}
		return MutationResult{}, newError(ErrConcurrentModification, "CONCURRENT_MODIFICATION", "The work item changed before the transition was committed.", map[string]any{"expected_version": input.ExpectedVersion})
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_item_events(public_id,work_item_id,actor_id,event_type,from_status,to_status,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7)`, eventPublicID, itemID, actor.ID, string(decision.EventType), string(item.Status), string(target), now); err != nil {
		return MutationResult{}, err
	}
	updated, err := queryWorkItem(ctx, tx, itemID)
	if err != nil {
		return MutationResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return MutationResult{}, err
	}
	return MutationResult{Item: updated}, nil
}
