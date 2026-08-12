package notifications

import (
	"context"
	"fmt"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/platform/id"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

type ListInput struct {
	Status     Status
	WorkItemID string
	Limit      int
	Cursor     string
}
type Page struct {
	Items      []Delivery `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}
type Repository interface {
	List(context.Context, ListInput) (Page, error)
	Get(context.Context, string) (Delivery, error)
	Replay(context.Context, string, time.Time) (Delivery, error)
}
type Service struct {
	repository Repository
	clock      clock.Clock
}

func NewService(repository Repository, current clock.Clock) (*Service, error) {
	if repository == nil || current == nil {
		return nil, fmt.Errorf("notification repository and clock are required")
	}
	return &Service{repository: repository, clock: current}, nil
}
func (service *Service) List(ctx context.Context, actor auth.Principal, input ListInput) (Page, error) {
	if err := workitems.AuthorizeManager(actor); err != nil {
		return Page{}, err
	}
	input, err := validateListInput(input)
	if err != nil {
		return Page{}, err
	}
	return service.repository.List(ctx, input)
}
func (service *Service) Get(ctx context.Context, actor auth.Principal, notificationID string) (Delivery, error) {
	if err := workitems.AuthorizeManager(actor); err != nil {
		return Delivery{}, err
	}
	if !id.IsUUID(notificationID) {
		return Delivery{}, fmt.Errorf("%w: notification id must be a UUID", ErrInvalidInput)
	}
	return service.repository.Get(ctx, notificationID)
}
func (service *Service) Replay(ctx context.Context, actor auth.Principal, notificationID string) (Delivery, error) {
	if err := workitems.AuthorizeManager(actor); err != nil {
		return Delivery{}, err
	}
	if !id.IsUUID(notificationID) {
		return Delivery{}, fmt.Errorf("%w: notification id must be a UUID", ErrInvalidInput)
	}
	return service.repository.Replay(ctx, notificationID, service.clock.Now())
}
func validateListInput(input ListInput) (ListInput, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 100 {
		return ListInput{}, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidInput)
	}
	switch input.Status {
	case "", StatusPending, StatusProcessing, StatusSent, StatusFailed:
	default:
		return ListInput{}, fmt.Errorf("%w: invalid status", ErrInvalidInput)
	}
	if input.WorkItemID != "" && !id.IsUUID(input.WorkItemID) {
		return ListInput{}, fmt.Errorf("%w: work_item_id must be a UUID", ErrInvalidInput)
	}
	if input.Cursor != "" {
		if _, err := DecodeListCursor(input.Cursor); err != nil {
			return ListInput{}, err
		}
	}
	return input, nil
}
