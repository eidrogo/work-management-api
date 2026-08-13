package notifications

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

func TestReplayRequiresReasonAndAttributesHTTPActor(t *testing.T) {
	t.Parallel()
	repository := &capturingRepository{replayResult: Delivery{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", DeliveryCycle: 2}}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Principal{ID: "manager-identity", Role: auth.RoleManager}
	notificationID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	got, err := service.Replay(context.Background(), manager, notificationID, ReplayInput{Reason: "  Manual investigation complete  ", RequestID: "request-http-1"})
	if err != nil || got.DeliveryCycle != 2 {
		t.Fatalf("delivery=%#v err=%v", got, err)
	}
	want := ReplayRequest{NotificationID: notificationID, ActorID: manager.ID, RequestID: "request-http-1", Channel: ReplayChannelHTTP, Reason: "Manual investigation complete"}
	if repository.captured != want {
		t.Fatalf("captured=%#v want=%#v", repository.captured, want)
	}

	worker := auth.Principal{ID: "worker-identity", Role: auth.RoleWorker}
	if _, err = service.Replay(context.Background(), worker, notificationID, ReplayInput{Reason: "retry", RequestID: "request-http-2"}); !errors.Is(err, workitems.ErrPermissionDenied) {
		t.Fatalf("worker error=%v", err)
	}

	for _, test := range []struct {
		name  string
		input ReplayInput
	}{
		{name: "missing reason", input: ReplayInput{RequestID: "request-http-3"}},
		{name: "blank reason", input: ReplayInput{Reason: " \t\n", RequestID: "request-http-3"}},
		{name: "reason over 1000 runes", input: ReplayInput{Reason: strings.Repeat("界", 1001), RequestID: "request-http-3"}},
		{name: "missing request id", input: ReplayInput{Reason: "retry"}},
		{name: "request id over 128 runes", input: ReplayInput{Reason: "retry", RequestID: strings.Repeat("r", 129)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Replay(context.Background(), manager, notificationID, test.input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestReplayRejectsNULAfterAuthorizationWithoutCallingRepository(t *testing.T) {
	t.Parallel()
	repository := &capturingRepository{}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	notificationID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	manager := auth.Principal{ID: "manager-identity", Role: auth.RoleManager}
	_, err = service.Replay(context.Background(), manager, notificationID, ReplayInput{Reason: "  provider\x00 recovered  ", RequestID: "request-http-nul"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("manager error=%v", err)
	}
	if repository.replayCalls != 0 {
		t.Fatalf("repository calls=%d", repository.replayCalls)
	}

	worker := auth.Principal{ID: "worker-identity", Role: auth.RoleWorker}
	_, err = service.Replay(context.Background(), worker, notificationID, ReplayInput{Reason: "provider\x00 recovered", RequestID: "request-http-worker"})
	if !errors.Is(err, workitems.ErrPermissionDenied) || errors.Is(err, ErrInvalidInput) {
		t.Fatalf("worker error=%v", err)
	}
	if repository.replayCalls != 0 {
		t.Fatalf("repository calls=%d", repository.replayCalls)
	}
}

func TestReplayAcceptsMaximumReasonAndOtherControls(t *testing.T) {
	t.Parallel()
	repository := &capturingRepository{replayResult: Delivery{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", DeliveryCycle: 2}}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Principal{ID: "manager-identity", Role: auth.RoleManager}
	notificationID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	reason := strings.Repeat("界", 998) + "\t界"
	if _, err = service.Replay(context.Background(), manager, notificationID, ReplayInput{Reason: reason, RequestID: "request-http-boundary"}); err != nil {
		t.Fatal(err)
	}
	if repository.captured.Reason != reason {
		t.Fatalf("reason length=%d", len([]rune(repository.captured.Reason)))
	}
}

type capturingRepository struct {
	captured     ReplayRequest
	replayResult Delivery
	replayCalls  int
}

func (*capturingRepository) List(context.Context, ListInput) (Page, error) {
	return Page{}, nil
}
func (*capturingRepository) Get(context.Context, string) (Delivery, error) {
	return Delivery{}, nil
}
func (repository *capturingRepository) Replay(_ context.Context, request ReplayRequest) (Delivery, error) {
	repository.replayCalls++
	repository.captured = request
	return repository.replayResult, nil
}
