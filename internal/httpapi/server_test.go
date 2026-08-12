package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/notifications"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

func TestAuthenticationFailureUsesMachineReadableError(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/work-items", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
	var body errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "AUTHENTICATION_REQUIRED" || body.Error.RequestID == "" {
		t.Fatalf("body=%#v", body)
	}
}
func TestHealthEndpoints(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s=%d", path, response.Code)
		}
	}
}
func newTestServer(t *testing.T) http.Handler {
	server, err := NewServer(ServerConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authenticator: fakeAuthenticator{}, WorkItems: &fakeWorkItems{}, Notifications: &fakeNotifications{}, Readiness: func(context.Context) error { return nil }, RequestID: func() (string, error) { return "request-test-id", nil }, MaxBodyBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

type fakeAuthenticator struct{}

func (fakeAuthenticator) Authenticate(context.Context, string) (auth.Principal, error) {
	return auth.Principal{}, auth.ErrInvalidCredentials
}

type fakeWorkItems struct{}

func (*fakeWorkItems) Create(context.Context, auth.Principal, workitems.CreateInput) (workitems.MutationResult, error) {
	return workitems.MutationResult{}, nil
}
func (*fakeWorkItems) Get(context.Context, auth.Principal, string) (workitems.WorkItem, error) {
	return workitems.WorkItem{}, nil
}
func (*fakeWorkItems) ListManager(context.Context, auth.Principal, workitems.ListInput) (workitems.Page, error) {
	return workitems.Page{}, nil
}
func (*fakeWorkItems) ListOwn(context.Context, auth.Principal, workitems.ListInput) (workitems.Page, error) {
	return workitems.Page{}, nil
}
func (*fakeWorkItems) Assign(context.Context, auth.Principal, string, workitems.AssignmentInput) (workitems.MutationResult, error) {
	return workitems.MutationResult{}, nil
}
func (*fakeWorkItems) Start(context.Context, auth.Principal, string, workitems.TransitionInput) (workitems.MutationResult, error) {
	return workitems.MutationResult{}, nil
}
func (*fakeWorkItems) Complete(context.Context, auth.Principal, string, workitems.TransitionInput) (workitems.MutationResult, error) {
	return workitems.MutationResult{}, nil
}
func (*fakeWorkItems) History(context.Context, auth.Principal, string, workitems.HistoryInput) (workitems.EventPage, error) {
	return workitems.EventPage{}, nil
}

type fakeNotifications struct{}

func (*fakeNotifications) List(context.Context, auth.Principal, notifications.ListInput) (notifications.Page, error) {
	return notifications.Page{}, nil
}
func (*fakeNotifications) Get(context.Context, auth.Principal, string) (notifications.Delivery, error) {
	return notifications.Delivery{}, nil
}
func (*fakeNotifications) Replay(context.Context, auth.Principal, string) (notifications.Delivery, error) {
	return notifications.Delivery{}, nil
}
