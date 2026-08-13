package httpapi

import (
	"context"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/notifications"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

type Authenticator interface {
	Authenticate(context.Context, string) (auth.Principal, error)
}
type WorkItemApplication interface {
	Create(context.Context, auth.Principal, workitems.CreateInput) (workitems.MutationResult, error)
	Get(context.Context, auth.Principal, string) (workitems.WorkItem, error)
	ListManager(context.Context, auth.Principal, workitems.ListInput) (workitems.Page, error)
	ListOwn(context.Context, auth.Principal, workitems.ListInput) (workitems.Page, error)
	Assign(context.Context, auth.Principal, string, workitems.AssignmentInput) (workitems.MutationResult, error)
	Start(context.Context, auth.Principal, string, workitems.TransitionInput) (workitems.MutationResult, error)
	Complete(context.Context, auth.Principal, string, workitems.TransitionInput) (workitems.MutationResult, error)
	History(context.Context, auth.Principal, string, workitems.HistoryInput) (workitems.EventPage, error)
}
type NotificationApplication interface {
	List(context.Context, auth.Principal, notifications.ListInput) (notifications.Page, error)
	Get(context.Context, auth.Principal, string) (notifications.Delivery, error)
	Replay(context.Context, auth.Principal, string, notifications.ReplayInput) (notifications.Delivery, error)
}
type MetricRecorder interface {
	RecordHTTP(string, string, int, time.Duration)
	RecordAuthFailure()
	RecordAuthorizationFailure()
	RecordMutation(string, string)
}
type ServerConfig struct {
	Logger         Logger
	Authenticator  Authenticator
	WorkItems      WorkItemApplication
	Notifications  NotificationApplication
	RequestID      func() (string, error)
	MaxBodyBytes   int64
	MetricRecorder MetricRecorder
}

type Logger interface {
	Debug(string, ...any)
	Info(string, ...any)
	Warn(string, ...any)
	Error(string, ...any)
}
