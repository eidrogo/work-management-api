package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
func TestPublicServerExcludesOperationalEndpoints(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/health/live", "/health/ready", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			assertPublicNotFound(t, response)
		})
	}
}

func TestPublicUnknownRoutesKeepStableEnvelope(t *testing.T) {
	server, err := NewServer(ServerConfig{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authenticator:  replayAuthenticator{principals: map[string]auth.Principal{"valid": {ID: "manager", Role: auth.RoleManager}}},
		WorkItems:      &fakeWorkItems{},
		Notifications:  &fakeNotifications{},
		RequestID:      func() (string, error) { return "request-test-id", nil },
		MaxBodyBytes:   1024,
		MetricRecorder: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/unrelated", "/v1/unknown"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer valid")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			assertPublicNotFound(t, response)
		})
	}
}

func assertPublicNotFound(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type=%q", contentType)
	}
	if requestID := response.Header().Get("X-Request-ID"); requestID != "request-test-id" {
		t.Fatalf("X-Request-ID=%q", requestID)
	}
	var body errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "RESOURCE_NOT_FOUND" || body.Error.RequestID != "request-test-id" {
		t.Fatalf("body=%#v", body)
	}
}

func TestReplayHTTPRequiresReasonAndAttributesRequest(t *testing.T) {
	notificationID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	manager := auth.Principal{ID: "manager-http", Role: auth.RoleManager}
	worker := auth.Principal{ID: "worker-http", Role: auth.RoleWorker}
	tests := []struct {
		name        string
		token       string
		body        string
		contentType string
		replayErr   error
		wantStatus  int
		wantCode    string
	}{
		{name: "missing body", token: "manager", contentType: "application/json", wantStatus: http.StatusBadRequest, wantCode: "INVALID_INPUT"},
		{name: "blank reason", token: "manager", body: `{"reason":"  "}`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantCode: "INVALID_INPUT"},
		{name: "reason over 1000 runes", token: "manager", body: `{"reason":"` + strings.Repeat("界", 1001) + `"}`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantCode: "INVALID_INPUT"},
		{name: "unknown field", token: "manager", body: `{"reason":"retry","actor_id":"spoofed"}`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantCode: "INVALID_INPUT"},
		{name: "unsupported media type", token: "manager", body: `{"reason":"retry"}`, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType, wantCode: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "worker denied", token: "worker", body: `{"reason":"retry"}`, contentType: "application/json", wantStatus: http.StatusForbidden, wantCode: "PERMISSION_DENIED"},
		{name: "cycle limit", token: "manager", body: `{"reason":"retry"}`, contentType: "application/json", replayErr: notifications.ErrReplayLimitReached, wantStatus: http.StatusConflict, wantCode: "NOTIFICATION_REPLAY_LIMIT_REACHED"},
		{name: "success", token: "manager", body: `{"reason":"  provider issue resolved  "}`, contentType: "application/json", wantStatus: http.StatusAccepted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &httpReplayRepository{delivery: notifications.Delivery{ID: notificationID, DeliveryCycle: 2}, replayErr: test.replayErr}
			service, err := notifications.NewService(repository)
			if err != nil {
				t.Fatal(err)
			}
			server, err := NewServer(ServerConfig{
				Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
				Authenticator: replayAuthenticator{principals: map[string]auth.Principal{"manager": manager, "worker": worker}},
				WorkItems:     &fakeWorkItems{}, Notifications: service,
				RequestID: func() (string, error) { return "generated-request", nil }, MaxBodyBytes: 1 << 20,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/notifications/"+notificationID+"/replay", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+test.token)
			request.Header.Set("X-Request-ID", "request-http-exact")
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantCode != "" {
				var body errorEnvelope
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error.Code != test.wantCode {
					t.Fatalf("code=%q body=%s", body.Error.Code, response.Body.String())
				}
			}
			if test.name == "success" {
				if repository.request.ActorID != manager.ID || repository.request.RequestID != "request-http-exact" || repository.request.Channel != notifications.ReplayChannelHTTP || repository.request.Reason != "provider issue resolved" {
					t.Fatalf("request=%#v", repository.request)
				}
			}
		})
	}
}

func TestReplayHTTPPersistsFallbackRequestID(t *testing.T) {
	notificationID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	repository := &httpReplayRepository{delivery: notifications.Delivery{ID: notificationID, DeliveryCycle: 2}}
	service, err := notifications.NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authenticator: replayAuthenticator{principals: map[string]auth.Principal{"manager": {ID: "manager-http", Role: auth.RoleManager}}},
		WorkItems:     &fakeWorkItems{}, Notifications: service,
		RequestID: func() (string, error) { return "", errors.New("entropy unavailable") }, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/notifications/"+notificationID+"/replay", strings.NewReader(`{"reason":"retry"}`))
	request.Header.Set("Authorization", "Bearer manager")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("X-Request-ID") != "request-id-unavailable" || repository.request.RequestID != "request-id-unavailable" {
		t.Fatalf("status=%d header=%q request=%#v", response.Code, response.Header().Get("X-Request-ID"), repository.request)
	}
}
func newTestServer(t *testing.T) http.Handler {
	server, err := NewServer(ServerConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authenticator: fakeAuthenticator{}, WorkItems: &fakeWorkItems{}, Notifications: &fakeNotifications{}, RequestID: func() (string, error) { return "request-test-id", nil }, MaxBodyBytes: 1024})
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
func (*fakeNotifications) Replay(context.Context, auth.Principal, string, notifications.ReplayInput) (notifications.Delivery, error) {
	return notifications.Delivery{}, nil
}

type replayAuthenticator struct{ principals map[string]auth.Principal }

func (authenticator replayAuthenticator) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	principal, ok := authenticator.principals[token]
	if !ok {
		return auth.Principal{}, auth.ErrInvalidCredentials
	}
	return principal, nil
}

type httpReplayRepository struct {
	request   notifications.ReplayRequest
	delivery  notifications.Delivery
	replayErr error
}

func (*httpReplayRepository) List(context.Context, notifications.ListInput) (notifications.Page, error) {
	return notifications.Page{}, nil
}
func (*httpReplayRepository) Get(context.Context, string) (notifications.Delivery, error) {
	return notifications.Delivery{}, nil
}
func (repository *httpReplayRepository) Replay(_ context.Context, request notifications.ReplayRequest) (notifications.Delivery, error) {
	repository.request = request
	return repository.delivery, repository.replayErr
}
