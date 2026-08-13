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

func TestHTTPPublicMethodsErrorsAndHeaders(t *testing.T) {
	itemID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	notificationID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	tests := []struct {
		name, method, path, body, call string
		workResult                     workitems.MutationResult
		wantStatus                     int
		wantETag, wantReplayed         string
	}{
		{name: "manager list", method: http.MethodGet, path: "/v1/work-items?status=ASSIGNED&assignee_id=cccccccc-cccc-4ccc-8ccc-cccccccccccc&limit=10&cursor=manager-cursor", call: "list-manager", wantStatus: http.StatusOK},
		{name: "create", method: http.MethodPost, path: "/v1/work-items", body: `{"title":"Inspect"}`, call: "create", workResult: workitems.MutationResult{Item: workitems.WorkItem{Version: 1}}, wantStatus: http.StatusCreated, wantETag: `"1"`},
		{name: "create replay", method: http.MethodPost, path: "/v1/work-items", body: `{"title":"Inspect"}`, call: "create", workResult: workitems.MutationResult{Item: workitems.WorkItem{Version: 1}, Idempotent: true}, wantStatus: http.StatusOK, wantETag: `"1"`, wantReplayed: "true"},
		{name: "worker list", method: http.MethodGet, path: "/v1/me/work-items?status=IN_PROGRESS&limit=11&cursor=worker-cursor", call: "list-own", wantStatus: http.StatusOK},
		{name: "get", method: http.MethodGet, path: "/v1/work-items/" + itemID, call: "get", workResult: workitems.MutationResult{Item: workitems.WorkItem{Version: 2}}, wantStatus: http.StatusOK, wantETag: `"2"`},
		{name: "assign replay", method: http.MethodPut, path: "/v1/work-items/" + itemID + "/assignment", body: `{"assignee_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc"}`, call: "assign", workResult: workitems.MutationResult{Item: workitems.WorkItem{Version: 2}, Idempotent: true}, wantStatus: http.StatusOK, wantETag: `"2"`, wantReplayed: "true"},
		{name: "start replay", method: http.MethodPost, path: "/v1/work-items/" + itemID + "/start", call: "start", workResult: workitems.MutationResult{Item: workitems.WorkItem{Version: 3}, Idempotent: true}, wantStatus: http.StatusOK, wantETag: `"3"`, wantReplayed: "true"},
		{name: "complete replay", method: http.MethodPost, path: "/v1/work-items/" + itemID + "/complete", call: "complete", workResult: workitems.MutationResult{Item: workitems.WorkItem{Version: 4}, Idempotent: true}, wantStatus: http.StatusOK, wantETag: `"4"`, wantReplayed: "true"},
		{name: "history", method: http.MethodGet, path: "/v1/work-items/" + itemID + "/history?limit=12&cursor=13", call: "history", wantStatus: http.StatusOK},
		{name: "notification list", method: http.MethodGet, path: "/v1/notifications?status=FAILED&work_item_id=" + itemID + "&limit=14&cursor=notification-cursor", call: "notification-list", wantStatus: http.StatusOK},
		{name: "notification get", method: http.MethodGet, path: "/v1/notifications/" + notificationID, call: "notification-get", wantStatus: http.StatusOK},
		{name: "notification replay", method: http.MethodPost, path: "/v1/notifications/" + notificationID + "/replay", body: `{"reason":"operator retry"}`, call: "notification-replay", wantStatus: http.StatusAccepted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workApplication := &recordingWorkItems{result: test.workResult}
			notificationApplication := &recordingNotifications{}
			server := newRecordingServer(t, workApplication, notificationApplication)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer valid")
			request.Header.Set("X-Request-ID", "request-public-matrix")
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if test.call == "assign" || test.call == "start" || test.call == "complete" {
				request.Header.Set("If-Match", `"1"`)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type=%q", got)
			}
			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options=%q", got)
			}
			if got := response.Header().Get("X-Request-ID"); got != "request-public-matrix" {
				t.Fatalf("X-Request-ID=%q", got)
			}
			if got := response.Header().Get("ETag"); got != test.wantETag {
				t.Fatalf("ETag=%q want=%q", got, test.wantETag)
			}
			if got := response.Header().Get("Idempotent-Replayed"); got != test.wantReplayed {
				t.Fatalf("Idempotent-Replayed=%q want=%q", got, test.wantReplayed)
			}
			gotCall := workApplication.call
			if notificationApplication.call != "" {
				gotCall = notificationApplication.call
			}
			if gotCall != test.call {
				t.Fatalf("application call=%q want=%q", gotCall, test.call)
			}
			assertRecordedHTTPInput(t, test.call, itemID, notificationID, workApplication, notificationApplication)
		})
	}

	t.Run("unauthorized error correlation", func(t *testing.T) {
		server := newTestServer(t)
		request := httptest.NewRequest(http.MethodGet, "/v1/work-items", nil)
		request.Header.Set("X-Request-ID", "request-unauthorized")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("status=%d WWW-Authenticate=%q", response.Code, response.Header().Get("WWW-Authenticate"))
		}
		var envelope errorEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Error.RequestID != response.Header().Get("X-Request-ID") {
			t.Fatalf("body request_id=%q header=%q", envelope.Error.RequestID, response.Header().Get("X-Request-ID"))
		}
	})

	for _, test := range []struct {
		name, method, path, allowed, code string
		wantStatus                        int
	}{
		{name: "wrong collection method", method: http.MethodDelete, path: "/v1/work-items", allowed: "GET, POST", code: "METHOD_NOT_ALLOWED", wantStatus: http.StatusMethodNotAllowed},
		{name: "wrong item method", method: http.MethodDelete, path: "/v1/work-items/" + itemID, allowed: "GET", code: "METHOD_NOT_ALLOWED", wantStatus: http.StatusMethodNotAllowed},
		{name: "unknown route", method: http.MethodGet, path: "/v1/unknown", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "unknown action", method: http.MethodGet, path: "/v1/work-items/" + itemID + "/unknown", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "deep route", method: http.MethodGet, path: "/v1/work-items/" + itemID + "/history/deep", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "work item collection trailing slash", method: http.MethodGet, path: "/v1/work-items/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "work item create trailing slash", method: http.MethodPost, path: "/v1/work-items/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "own work item collection trailing slash", method: http.MethodGet, path: "/v1/me/work-items/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "work item trailing slash", method: http.MethodGet, path: "/v1/work-items/" + itemID + "/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "assignment trailing slash", method: http.MethodPut, path: "/v1/work-items/" + itemID + "/assignment/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "start trailing slash", method: http.MethodPost, path: "/v1/work-items/" + itemID + "/start/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "complete trailing slash", method: http.MethodPost, path: "/v1/work-items/" + itemID + "/complete/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "history trailing slash", method: http.MethodGet, path: "/v1/work-items/" + itemID + "/history/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "notification collection trailing slash", method: http.MethodGet, path: "/v1/notifications/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "notification trailing slash", method: http.MethodGet, path: "/v1/notifications/" + notificationID + "/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "notification replay trailing slash", method: http.MethodPost, path: "/v1/notifications/" + notificationID + "/replay/", code: "RESOURCE_NOT_FOUND", wantStatus: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			workApplication := &recordingWorkItems{}
			notificationApplication := &recordingNotifications{}
			server := newRecordingServer(t, workApplication, notificationApplication)
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Authorization", "Bearer valid")
			request.Header.Set("X-Request-ID", "request-route-contract")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			var envelope errorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			requestID := response.Header().Get("X-Request-ID")
			if response.Code != test.wantStatus || envelope.Error.Code != test.code || requestID != "request-route-contract" || envelope.Error.RequestID != requestID {
				t.Fatalf("status=%d body=%#v", response.Code, envelope)
			}
			if test.allowed != "" && envelope.Error.Details["allowed"] != test.allowed {
				t.Fatalf("allowed=%v want=%q", envelope.Error.Details["allowed"], test.allowed)
			}
			if workApplication.call != "" || notificationApplication.call != "" {
				t.Fatalf("unexpected application calls: work=%q notification=%q", workApplication.call, notificationApplication.call)
			}
		})
	}

	for _, requestID := range []string{"", "contains spaces", strings.Repeat("x", 129)} {
		t.Run("invalid request id is replaced", func(t *testing.T) {
			server := newRecordingServer(t, &recordingWorkItems{}, &recordingNotifications{})
			request := httptest.NewRequest(http.MethodGet, "/v1/unknown", nil)
			request.Header.Set("Authorization", "Bearer valid")
			request.Header.Set("X-Request-ID", requestID)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			var envelope errorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if got := response.Header().Get("X-Request-ID"); got != "request-generated" || envelope.Error.RequestID != got {
				t.Fatalf("header=%q body=%q", got, envelope.Error.RequestID)
			}
		})
	}
}

func TestHTTPRejectsMalformedBodiesAndPreconditionsBeforeMutation(t *testing.T) {
	itemID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	tests := []struct {
		name, method, path, body, contentType, ifMatch string
		maxBodyBytes                                   int64
		wantStatus                                     int
	}{
		{name: "missing media type", method: http.MethodPost, path: "/v1/work-items", body: `{}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "wrong media type", method: http.MethodPost, path: "/v1/work-items", body: `{}`, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
		{name: "empty body", method: http.MethodPost, path: "/v1/work-items", contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, path: "/v1/work-items", body: `{"title":"Inspect","unknown":true}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "second JSON value", method: http.MethodPost, path: "/v1/work-items", body: `{"title":"Inspect"}{}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "oversized body", method: http.MethodPost, path: "/v1/work-items", body: `{"title":"This is too large"}`, contentType: "application/json", maxBodyBytes: 8, wantStatus: http.StatusBadRequest},
		{name: "assignment missing media type", method: http.MethodPut, path: "/v1/work-items/" + itemID + "/assignment", body: `{"assignee_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}`, ifMatch: `"1"`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "assignment empty body", method: http.MethodPut, path: "/v1/work-items/" + itemID + "/assignment", contentType: "application/json", ifMatch: `"1"`, wantStatus: http.StatusBadRequest},
		{name: "assignment unknown field", method: http.MethodPut, path: "/v1/work-items/" + itemID + "/assignment", body: `{"assignee_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","unknown":true}`, contentType: "application/json", ifMatch: `"1"`, wantStatus: http.StatusBadRequest},
		{name: "assignment second JSON value", method: http.MethodPut, path: "/v1/work-items/" + itemID + "/assignment", body: `{"assignee_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}{}`, contentType: "application/json", ifMatch: `"1"`, wantStatus: http.StatusBadRequest},
		{name: "assignment oversized body", method: http.MethodPut, path: "/v1/work-items/" + itemID + "/assignment", body: `{"assignee_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}`, contentType: "application/json", ifMatch: `"1"`, maxBodyBytes: 8, wantStatus: http.StatusBadRequest},
		{name: "assignment malformed If-Match", method: http.MethodPut, path: "/v1/work-items/" + itemID + "/assignment", body: `{"assignee_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}`, contentType: "application/json", ifMatch: "1", wantStatus: http.StatusBadRequest},
		{name: "replay missing media type", method: http.MethodPost, path: "/v1/notifications/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/replay", body: `{"reason":"retry"}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "replay empty body", method: http.MethodPost, path: "/v1/notifications/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/replay", contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "replay unknown field", method: http.MethodPost, path: "/v1/notifications/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/replay", body: `{"reason":"retry","unknown":true}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "replay second JSON value", method: http.MethodPost, path: "/v1/notifications/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/replay", body: `{"reason":"retry"}{}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "replay oversized body", method: http.MethodPost, path: "/v1/notifications/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb/replay", body: `{"reason":"retry"}`, contentType: "application/json", maxBodyBytes: 8, wantStatus: http.StatusBadRequest},
		{name: "start nonpositive If-Match", method: http.MethodPost, path: "/v1/work-items/" + itemID + "/start", ifMatch: `"0"`, wantStatus: http.StatusBadRequest},
		{name: "complete malformed If-Match", method: http.MethodPost, path: "/v1/work-items/" + itemID + "/complete", ifMatch: `"not-a-version"`, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workApplication := &recordingWorkItems{}
			notificationApplication := &recordingNotifications{}
			server := newRecordingServerWithMaxBody(t, workApplication, notificationApplication, test.maxBodyBytes)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer valid")
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.ifMatch != "" {
				request.Header.Set("If-Match", test.ifMatch)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if workApplication.call != "" {
				t.Fatalf("mutation application was called: %q", workApplication.call)
			}
			if notificationApplication.call != "" {
				t.Fatalf("notification mutation application was called: %q", notificationApplication.call)
			}
		})
	}

	t.Run("missing If-Match reaches idempotency decision", func(t *testing.T) {
		application := &recordingWorkItems{result: workitems.MutationResult{Item: workitems.WorkItem{Version: 2}, Idempotent: true}}
		server := newRecordingServer(t, application, &recordingNotifications{})
		request := httptest.NewRequest(http.MethodPost, "/v1/work-items/"+itemID+"/start", nil)
		request.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK || application.transitionInput.ExpectedVersion != 0 {
			t.Fatalf("status=%d expected_version=%d body=%s", response.Code, application.transitionInput.ExpectedVersion, response.Body.String())
		}
	})

	t.Run("state change without If-Match has stable error", func(t *testing.T) {
		application := &recordingWorkItems{err: workitems.ErrExpectedVersionRequired}
		server := newRecordingServer(t, application, &recordingNotifications{})
		request := httptest.NewRequest(http.MethodPost, "/v1/work-items/"+itemID+"/start", nil)
		request.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"EXPECTED_VERSION_REQUIRED"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestHTTPMapsStableDomainErrorsWithoutLeakingCauses(t *testing.T) {
	secret := errors.New("provider-secret sql-detail")
	tests := []struct {
		name, code, message string
		err                 error
		want                int
	}{
		{name: "authentication unavailable", err: errors.Join(auth.ErrAuthenticationUnavailable, secret), code: "AUTHENTICATION_UNAVAILABLE", message: "Authentication is temporarily unavailable.", want: http.StatusServiceUnavailable},
		{name: "permission", err: errors.Join(workitems.ErrPermissionDenied, secret), code: "PERMISSION_DENIED", message: "You are not allowed to perform this action.", want: http.StatusForbidden},
		{name: "work item hidden", err: errors.Join(workitems.ErrResourceNotFound, secret), code: "RESOURCE_NOT_FOUND", message: "The requested resource was not found.", want: http.StatusNotFound},
		{name: "notification hidden", err: errors.Join(notifications.ErrResourceNotFound, secret), code: "RESOURCE_NOT_FOUND", message: "The requested resource was not found.", want: http.StatusNotFound},
		{name: "work item invalid", err: errors.Join(workitems.ErrInvalidInput, secret), code: "INVALID_INPUT", message: "The request contains invalid input.", want: http.StatusBadRequest},
		{name: "notification invalid", err: errors.Join(notifications.ErrInvalidInput, secret), code: "INVALID_INPUT", message: "The request contains invalid input.", want: http.StatusBadRequest},
		{name: "idempotency key required", err: errors.Join(workitems.ErrIdempotencyKeyRequired, secret), code: "IDEMPOTENCY_KEY_REQUIRED", message: "An Idempotency-Key header is required.", want: http.StatusBadRequest},
		{name: "expected version required", err: errors.Join(workitems.ErrExpectedVersionRequired, secret), code: "EXPECTED_VERSION_REQUIRED", message: "A positive expected version is required.", want: http.StatusBadRequest},
		{name: "assignee", err: errors.Join(workitems.ErrAssigneeMustBeWorker, secret), code: "ASSIGNEE_MUST_BE_WORKER", message: "The assignee must reference an existing worker.", want: http.StatusUnprocessableEntity},
		{name: "reassignment", err: errors.Join(workitems.ErrReassignmentReasonRequired, secret), code: "REASSIGNMENT_REASON_REQUIRED", message: "A reason is required when the assignee changes.", want: http.StatusUnprocessableEntity},
		{name: "transition", err: errors.Join(workitems.ErrInvalidTransition, secret), code: "WORK_ITEM_INVALID_TRANSITION", message: "The requested work-item transition is not allowed.", want: http.StatusConflict},
		{name: "assignment conflict", err: errors.Join(workitems.ErrAssignmentConflict, secret), code: "ASSIGNMENT_CONFLICT", message: "The work item cannot be assigned in its current state.", want: http.StatusConflict},
		{name: "immutable", err: errors.Join(workitems.ErrCompletedImmutable, secret), code: "WORK_ITEM_COMPLETED_IMMUTABLE", message: "Completed work cannot be changed.", want: http.StatusConflict},
		{name: "concurrent", err: errors.Join(workitems.ErrConcurrentModification, secret), code: "CONCURRENT_MODIFICATION", message: "The work item changed after it was read.", want: http.StatusConflict},
		{name: "idempotency", err: errors.Join(workitems.ErrIdempotencyKeyReused, secret), code: "IDEMPOTENCY_KEY_REUSED", message: "The idempotency key was already used with a different request.", want: http.StatusConflict},
		{name: "notification state", err: errors.Join(notifications.ErrNotReplayable, secret), code: "NOTIFICATION_NOT_RETRYABLE", message: "Only a terminally failed notification can be replayed.", want: http.StatusConflict},
		{name: "notification lease", err: errors.Join(notifications.ErrLeaseLost, secret), code: "NOTIFICATION_LEASE_LOST", message: "The notification is no longer owned by this worker.", want: http.StatusConflict},
		{name: "notification limit", err: errors.Join(notifications.ErrReplayLimitReached, secret), code: "NOTIFICATION_REPLAY_LIMIT_REACHED", message: "The notification cannot be replayed after 10 delivery cycles.", want: http.StatusConflict},
		{name: "internal", err: secret, code: "INTERNAL_ERROR", message: "An internal error occurred.", want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := mapError(test.err)
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if status != test.want || body.Code != test.code || body.Message != test.message {
				t.Fatalf("status=%d code=%q body=%s", status, body.Code, encoded)
			}
			if strings.Contains(string(encoded), "provider-secret") || strings.Contains(string(encoded), "sql-detail") {
				t.Fatalf("error leaked cause: %s", encoded)
			}
		})
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

type recordingWorkItems struct {
	call            string
	result          workitems.MutationResult
	err             error
	itemID          string
	createInput     workitems.CreateInput
	listInput       workitems.ListInput
	assignmentInput workitems.AssignmentInput
	transitionInput workitems.TransitionInput
	historyInput    workitems.HistoryInput
}

func (application *recordingWorkItems) Create(_ context.Context, _ auth.Principal, input workitems.CreateInput) (workitems.MutationResult, error) {
	application.call = "create"
	application.createInput = input
	return application.result, application.err
}
func (application *recordingWorkItems) Get(_ context.Context, _ auth.Principal, itemID string) (workitems.WorkItem, error) {
	application.call = "get"
	application.itemID = itemID
	return application.result.Item, application.err
}
func (application *recordingWorkItems) ListManager(_ context.Context, _ auth.Principal, input workitems.ListInput) (workitems.Page, error) {
	application.call = "list-manager"
	application.listInput = input
	return workitems.Page{}, application.err
}
func (application *recordingWorkItems) ListOwn(_ context.Context, _ auth.Principal, input workitems.ListInput) (workitems.Page, error) {
	application.call = "list-own"
	application.listInput = input
	return workitems.Page{}, application.err
}
func (application *recordingWorkItems) Assign(_ context.Context, _ auth.Principal, itemID string, input workitems.AssignmentInput) (workitems.MutationResult, error) {
	application.call = "assign"
	application.itemID = itemID
	application.assignmentInput = input
	return application.result, application.err
}
func (application *recordingWorkItems) Start(_ context.Context, _ auth.Principal, itemID string, input workitems.TransitionInput) (workitems.MutationResult, error) {
	application.call = "start"
	application.itemID = itemID
	application.transitionInput = input
	return application.result, application.err
}
func (application *recordingWorkItems) Complete(_ context.Context, _ auth.Principal, itemID string, input workitems.TransitionInput) (workitems.MutationResult, error) {
	application.call = "complete"
	application.itemID = itemID
	application.transitionInput = input
	return application.result, application.err
}
func (application *recordingWorkItems) History(_ context.Context, _ auth.Principal, itemID string, input workitems.HistoryInput) (workitems.EventPage, error) {
	application.call = "history"
	application.itemID = itemID
	application.historyInput = input
	return workitems.EventPage{}, application.err
}

type recordingNotifications struct {
	call           string
	err            error
	notificationID string
	listInput      notifications.ListInput
	replayInput    notifications.ReplayInput
}

func (application *recordingNotifications) List(_ context.Context, _ auth.Principal, input notifications.ListInput) (notifications.Page, error) {
	application.call = "notification-list"
	application.listInput = input
	return notifications.Page{}, application.err
}
func (application *recordingNotifications) Get(_ context.Context, _ auth.Principal, notificationID string) (notifications.Delivery, error) {
	application.call = "notification-get"
	application.notificationID = notificationID
	return notifications.Delivery{}, application.err
}
func (application *recordingNotifications) Replay(_ context.Context, _ auth.Principal, notificationID string, input notifications.ReplayInput) (notifications.Delivery, error) {
	application.call = "notification-replay"
	application.notificationID = notificationID
	application.replayInput = input
	return notifications.Delivery{}, application.err
}

func assertRecordedHTTPInput(t *testing.T, call, itemID, notificationID string, work *recordingWorkItems, notification *recordingNotifications) {
	t.Helper()
	switch call {
	case "list-manager":
		if work.listInput.Status != workitems.StatusAssigned || work.listInput.AssigneeID != "cccccccc-cccc-4ccc-8ccc-cccccccccccc" || work.listInput.Limit != 10 || work.listInput.Cursor != "manager-cursor" {
			t.Fatalf("manager list input=%#v", work.listInput)
		}
	case "create":
		if work.createInput.Title != "Inspect" {
			t.Fatalf("create input=%#v", work.createInput)
		}
	case "list-own":
		if work.listInput.Status != workitems.StatusInProgress || work.listInput.Limit != 11 || work.listInput.Cursor != "worker-cursor" {
			t.Fatalf("worker list input=%#v", work.listInput)
		}
	case "get":
		if work.itemID != itemID {
			t.Fatalf("get item id=%q", work.itemID)
		}
	case "assign":
		if work.itemID != itemID || work.assignmentInput.AssigneeID != "cccccccc-cccc-4ccc-8ccc-cccccccccccc" || work.assignmentInput.ExpectedVersion != 1 {
			t.Fatalf("assign item=%q input=%#v", work.itemID, work.assignmentInput)
		}
	case "start", "complete":
		if work.itemID != itemID || work.transitionInput.ExpectedVersion != 1 {
			t.Fatalf("transition item=%q input=%#v", work.itemID, work.transitionInput)
		}
	case "history":
		if work.itemID != itemID || work.historyInput.Limit != 12 || work.historyInput.Cursor != "13" {
			t.Fatalf("history item=%q input=%#v", work.itemID, work.historyInput)
		}
	case "notification-list":
		if notification.listInput.Status != notifications.StatusFailed || notification.listInput.WorkItemID != itemID || notification.listInput.Limit != 14 || notification.listInput.Cursor != "notification-cursor" {
			t.Fatalf("notification list input=%#v", notification.listInput)
		}
	case "notification-get":
		if notification.notificationID != notificationID {
			t.Fatalf("notification id=%q", notification.notificationID)
		}
	case "notification-replay":
		if notification.notificationID != notificationID || notification.replayInput.Reason != "operator retry" || notification.replayInput.RequestID != "request-public-matrix" {
			t.Fatalf("notification id=%q replay input=%#v", notification.notificationID, notification.replayInput)
		}
	default:
		t.Fatalf("unknown recorded call %q", call)
	}
}

func newRecordingServer(t *testing.T, workItems WorkItemApplication, notificationApplication NotificationApplication) http.Handler {
	t.Helper()
	return newRecordingServerWithMaxBody(t, workItems, notificationApplication, 1024)
}

func newRecordingServerWithMaxBody(t *testing.T, workItems WorkItemApplication, notificationApplication NotificationApplication, maxBodyBytes int64) http.Handler {
	t.Helper()
	if maxBodyBytes == 0 {
		maxBodyBytes = 1024
	}
	server, err := NewServer(ServerConfig{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authenticator:  replayAuthenticator{principals: map[string]auth.Principal{"valid": {ID: "manager", Role: auth.RoleManager}}},
		WorkItems:      workItems,
		Notifications:  notificationApplication,
		RequestID:      func() (string, error) { return "request-generated", nil },
		MaxBodyBytes:   maxBodyBytes,
		MetricRecorder: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}
