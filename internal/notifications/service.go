package notifications

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/platform/id"
	"github.com/eidrogo/work-management-api/internal/platform/persistedtext"
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
	Replay(context.Context, ReplayRequest) (Delivery, error)
}
type ReplayInput struct {
	Reason    string
	RequestID string
}
type ReplayRequest struct {
	NotificationID string
	ActorID        string
	RequestID      string
	Channel        ReplayChannel
	Reason         string
}
type Service struct{ repository Repository }

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("notification repository is required")
	}
	return &Service{repository: repository}, nil
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
func (service *Service) Replay(ctx context.Context, actor auth.Principal, notificationID string, input ReplayInput) (Delivery, error) {
	if err := workitems.AuthorizeManager(actor); err != nil {
		return Delivery{}, err
	}
	request, err := validateReplayRequest(ReplayRequest{
		NotificationID: notificationID,
		ActorID:        actor.ID,
		RequestID:      input.RequestID,
		Channel:        ReplayChannelHTTP,
		Reason:         input.Reason,
	})
	if err != nil {
		return Delivery{}, err
	}
	return service.repository.Replay(ctx, request)
}

func validateReplayRequest(request ReplayRequest) (ReplayRequest, error) {
	request.NotificationID = strings.TrimSpace(request.NotificationID)
	request.ActorID = strings.TrimSpace(request.ActorID)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.Reason = strings.TrimSpace(request.Reason)
	if !id.IsUUID(request.NotificationID) {
		return ReplayRequest{}, fmt.Errorf("%w: notification id must be a UUID", ErrInvalidInput)
	}
	if request.ActorID == "" || utf8.RuneCountInString(request.ActorID) > 200 {
		return ReplayRequest{}, fmt.Errorf("%w: actor id must contain between 1 and 200 characters", ErrInvalidInput)
	}
	if request.RequestID == "" || utf8.RuneCountInString(request.RequestID) > 128 {
		return ReplayRequest{}, fmt.Errorf("%w: request id must contain between 1 and 128 characters", ErrInvalidInput)
	}
	if request.Channel != ReplayChannelHTTP && request.Channel != ReplayChannelCLI {
		return ReplayRequest{}, fmt.Errorf("%w: invalid replay channel", ErrInvalidInput)
	}
	if request.Reason == "" || utf8.RuneCountInString(request.Reason) > 1000 {
		return ReplayRequest{}, fmt.Errorf("%w: replay reason must contain between 1 and 1000 characters", ErrInvalidInput)
	}
	if err := persistedtext.Validate(request.Reason); err != nil {
		return ReplayRequest{}, newInvalidInput("Reason contains an unsupported character.", map[string]any{"field": "reason"})
	}
	return request, nil
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
