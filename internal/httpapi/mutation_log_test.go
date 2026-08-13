package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/notifications"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

const (
	mutationActorManagerID = "11111111-1111-4111-8111-111111111111"
	mutationActorWorkerID  = "22222222-2222-4222-8222-222222222222"
	mutationRouteItemID    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	mutationResultItemID   = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	mutationNotificationID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	mutationAssigneeID     = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

	mutationTokenSentinel      = "token-private-sentinel"
	mutationIdempotencySecret  = "idempotency-private-sentinel"
	mutationEmailSentinel      = "email-private-sentinel@example.test"
	mutationNameSentinel       = "name-private-sentinel"
	mutationTitleSentinel      = "title-private-sentinel"
	mutationDescriptionSecret  = "description-private-sentinel"
	mutationAssignmentSecret   = "assignment-reason-private-sentinel"
	mutationReplayReasonSecret = "replay-reason-private-sentinel"
	mutationProviderSecret     = "provider-private-sentinel"
	mutationSQLSecret          = "sql-private-sentinel"
)

var mutationPrivateSentinels = []string{
	mutationTokenSentinel,
	mutationIdempotencySecret,
	mutationEmailSentinel,
	mutationNameSentinel,
	mutationTitleSentinel,
	mutationDescriptionSecret,
	mutationAssignmentSecret,
	mutationReplayReasonSecret,
	mutationProviderSecret,
	mutationSQLSecret,
}

type mutationLogExpectation struct {
	requestID, actorID, operation, outcome string
	workItemID, notificationID             string
	idempotent                             *bool
	status                                 int
	errorCode, level                       string
}

func TestAPIMutationLogsSuccessfulAndIdempotentResults(t *testing.T) {
	falseValue, trueValue := false, true
	manager := mutationLogPrincipal(mutationActorManagerID, auth.RoleManager)
	worker := mutationLogPrincipal(mutationActorWorkerID, auth.RoleWorker)
	item := workitems.WorkItem{
		ID:           mutationResultItemID,
		Title:        mutationTitleSentinel,
		Description:  mutationDescriptionSecret,
		CreatorName:  mutationNameSentinel,
		AssigneeName: mutationNameSentinel,
		Version:      2,
	}
	delivery := notifications.Delivery{
		ID:                mutationNotificationID,
		WorkItemID:        mutationResultItemID,
		RecipientEmail:    mutationEmailSentinel,
		RecipientName:     mutationNameSentinel,
		Payload:           json.RawMessage(`{"private":"` + mutationDescriptionSecret + `"}`),
		LastError:         mutationProviderSecret,
		ProviderMessageID: mutationSQLSecret,
	}
	tests := []struct {
		name, method, path, body, requestID string
		principal                           auth.Principal
		ifMatch                             string
		workResult                          workitems.MutationResult
		notificationResult                  notifications.Delivery
		wantStatus                          int
		want                                mutationLogExpectation
	}{
		{
			name: "create", method: http.MethodPost, path: "/v1/work-items",
			body:      `{"title":"` + mutationTitleSentinel + `","description":"` + mutationDescriptionSecret + `"}`,
			requestID: "request-mutation-create", principal: manager,
			workResult: workitems.MutationResult{Item: item}, wantStatus: http.StatusCreated,
			want: mutationLogExpectation{actorID: manager.ID, operation: "create", outcome: "success", workItemID: item.ID, idempotent: &falseValue, level: "INFO"},
		},
		{
			name: "idempotent create replay", method: http.MethodPost, path: "/v1/work-items",
			body:      `{"title":"` + mutationTitleSentinel + `","description":"` + mutationDescriptionSecret + `"}`,
			requestID: "request-mutation-create-replay", principal: manager,
			workResult: workitems.MutationResult{Item: item, Idempotent: true}, wantStatus: http.StatusOK,
			want: mutationLogExpectation{actorID: manager.ID, operation: "create", outcome: "success", workItemID: item.ID, idempotent: &trueValue, level: "INFO"},
		},
		{
			name: "assignment", method: http.MethodPut, path: "/v1/work-items/" + mutationRouteItemID + "/assignment",
			body:      `{"assignee_id":"` + mutationAssigneeID + `","reason":"` + mutationAssignmentSecret + `"}`,
			requestID: "request-mutation-assignment", principal: manager, ifMatch: `"1"`,
			workResult: workitems.MutationResult{Item: item}, wantStatus: http.StatusOK,
			want: mutationLogExpectation{actorID: manager.ID, operation: "assignment", outcome: "success", workItemID: item.ID, idempotent: &falseValue, level: "INFO"},
		},
		{
			name: "same assignee no-op", method: http.MethodPut, path: "/v1/work-items/" + mutationRouteItemID + "/assignment",
			body:      `{"assignee_id":"` + mutationAssigneeID + `","reason":"` + mutationAssignmentSecret + `"}`,
			requestID: "request-mutation-assignment-noop", principal: manager, ifMatch: `"1"`,
			workResult: workitems.MutationResult{Item: item, Idempotent: true}, wantStatus: http.StatusOK,
			want: mutationLogExpectation{actorID: manager.ID, operation: "assignment", outcome: "success", workItemID: item.ID, idempotent: &trueValue, level: "INFO"},
		},
		{
			name: "start", method: http.MethodPost, path: "/v1/work-items/" + mutationRouteItemID + "/start",
			requestID: "request-mutation-start", principal: worker, ifMatch: `"1"`,
			workResult: workitems.MutationResult{Item: item}, wantStatus: http.StatusOK,
			want: mutationLogExpectation{actorID: worker.ID, operation: "start", outcome: "success", workItemID: item.ID, idempotent: &falseValue, level: "INFO"},
		},
		{
			name: "idempotent start replay", method: http.MethodPost, path: "/v1/work-items/" + mutationRouteItemID + "/start",
			requestID: "request-mutation-start-replay", principal: worker, ifMatch: `"1"`,
			workResult: workitems.MutationResult{Item: item, Idempotent: true}, wantStatus: http.StatusOK,
			want: mutationLogExpectation{actorID: worker.ID, operation: "start", outcome: "success", workItemID: item.ID, idempotent: &trueValue, level: "INFO"},
		},
		{
			name: "complete", method: http.MethodPost, path: "/v1/work-items/" + mutationRouteItemID + "/complete",
			requestID: "request-mutation-complete", principal: worker, ifMatch: `"2"`,
			workResult: workitems.MutationResult{Item: item}, wantStatus: http.StatusOK,
			want: mutationLogExpectation{actorID: worker.ID, operation: "complete", outcome: "success", workItemID: item.ID, idempotent: &falseValue, level: "INFO"},
		},
		{
			name: "idempotent complete replay", method: http.MethodPost, path: "/v1/work-items/" + mutationRouteItemID + "/complete",
			requestID: "request-mutation-complete-replay", principal: worker, ifMatch: `"2"`,
			workResult: workitems.MutationResult{Item: item, Idempotent: true}, wantStatus: http.StatusOK,
			want: mutationLogExpectation{actorID: worker.ID, operation: "complete", outcome: "success", workItemID: item.ID, idempotent: &trueValue, level: "INFO"},
		},
		{
			name: "notification replay", method: http.MethodPost, path: "/v1/notifications/" + mutationNotificationID + "/replay",
			body:      `{"reason":"` + mutationReplayReasonSecret + `"}`,
			requestID: "request-mutation-notification-replay", principal: manager,
			notificationResult: delivery, wantStatus: http.StatusAccepted,
			want: mutationLogExpectation{actorID: manager.ID, operation: "notification_replay", outcome: "success", workItemID: delivery.WorkItemID, notificationID: mutationNotificationID, level: "INFO"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			server := newMutationLogTestServer(t, &output, test.principal,
				&mutationLogWorkItems{result: test.workResult},
				&mutationLogNotifications{result: test.notificationResult},
			)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+mutationTokenSentinel)
			request.Header.Set("X-Request-ID", test.requestID)
			request.Header.Set("Idempotency-Key", mutationIdempotencySecret)
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if test.ifMatch != "" {
				request.Header.Set("If-Match", test.ifMatch)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}

			test.want.requestID = test.requestID
			assertMutationLogOutput(t, output.String(), test.want)
		})
	}
}

func TestAPIMutationLogsStableFailuresWithoutPrivateData(t *testing.T) {
	manager := mutationLogPrincipal(mutationActorManagerID, auth.RoleManager)
	worker := mutationLogPrincipal(mutationActorWorkerID, auth.RoleWorker)
	privateCause := errors.New(mutationProviderSecret + " " + mutationSQLSecret)
	tests := []struct {
		name, method, path, body, requestID, ifMatch string
		principal                                    auth.Principal
		workErr, notificationErr                     error
		wantStatus                                   int
		want                                         mutationLogExpectation
	}{
		{
			name: "create decode invalid", method: http.MethodPost, path: "/v1/work-items", body: `{`, requestID: "request-mutation-create-decode", principal: manager,
			wantStatus: http.StatusBadRequest,
			want:       mutationLogExpectation{actorID: manager.ID, operation: "create", outcome: "failure", status: http.StatusBadRequest, errorCode: "INVALID_INPUT", level: "WARN"},
		},
		{
			name: "assignment If-Match invalid", method: http.MethodPut, path: "/v1/work-items/" + mutationRouteItemID + "/assignment",
			body:      `{"assignee_id":"` + mutationAssigneeID + `","reason":"` + mutationAssignmentSecret + `"}`,
			requestID: "request-mutation-assignment-if-match", ifMatch: "invalid", principal: manager,
			wantStatus: http.StatusBadRequest,
			want:       mutationLogExpectation{actorID: manager.ID, operation: "assignment", outcome: "failure", workItemID: mutationRouteItemID, status: http.StatusBadRequest, errorCode: "INVALID_INPUT", level: "WARN"},
		},
		{
			name: "assignment permission denied", method: http.MethodPut, path: "/v1/work-items/" + mutationRouteItemID + "/assignment",
			body:      `{"assignee_id":"` + mutationAssigneeID + `","reason":"` + mutationAssignmentSecret + `"}`,
			requestID: "request-mutation-assignment-denied", ifMatch: `"1"`, principal: worker,
			workErr: errors.Join(workitems.ErrPermissionDenied, privateCause), wantStatus: http.StatusForbidden,
			want: mutationLogExpectation{actorID: worker.ID, operation: "assignment", outcome: "failure", workItemID: mutationRouteItemID, status: http.StatusForbidden, errorCode: "PERMISSION_DENIED", level: "WARN"},
		},
		{
			name: "start not found", method: http.MethodPost, path: "/v1/work-items/" + mutationRouteItemID + "/start",
			requestID: "request-mutation-start-not-found", ifMatch: `"1"`, principal: worker,
			workErr: errors.Join(workitems.ErrResourceNotFound, privateCause), wantStatus: http.StatusNotFound,
			want: mutationLogExpectation{actorID: worker.ID, operation: "start", outcome: "failure", workItemID: mutationRouteItemID, status: http.StatusNotFound, errorCode: "RESOURCE_NOT_FOUND", level: "WARN"},
		},
		{
			name: "complete conflict", method: http.MethodPost, path: "/v1/work-items/" + mutationRouteItemID + "/complete",
			requestID: "request-mutation-complete-conflict", ifMatch: `"2"`, principal: worker,
			workErr: errors.Join(workitems.ErrAssignmentConflict, privateCause), wantStatus: http.StatusConflict,
			want: mutationLogExpectation{actorID: worker.ID, operation: "complete", outcome: "failure", workItemID: mutationRouteItemID, status: http.StatusConflict, errorCode: "ASSIGNMENT_CONFLICT", level: "WARN"},
		},
		{
			name: "complete internal", method: http.MethodPost, path: "/v1/work-items/" + mutationRouteItemID + "/complete",
			requestID: "request-mutation-complete-internal", ifMatch: `"2"`, principal: worker,
			workErr: privateCause, wantStatus: http.StatusInternalServerError,
			want: mutationLogExpectation{actorID: worker.ID, operation: "complete", outcome: "failure", workItemID: mutationRouteItemID, status: http.StatusInternalServerError, errorCode: "INTERNAL_ERROR", level: "ERROR"},
		},
		{
			name: "notification decode invalid", method: http.MethodPost, path: "/v1/notifications/" + mutationNotificationID + "/replay",
			body: `{`, requestID: "request-mutation-notification-decode", principal: manager,
			wantStatus: http.StatusBadRequest,
			want:       mutationLogExpectation{actorID: manager.ID, operation: "notification_replay", outcome: "failure", notificationID: mutationNotificationID, status: http.StatusBadRequest, errorCode: "INVALID_INPUT", level: "WARN"},
		},
		{
			name: "notification permission denied", method: http.MethodPost, path: "/v1/notifications/" + mutationNotificationID + "/replay",
			body: `{"reason":"` + mutationReplayReasonSecret + `"}`, requestID: "request-mutation-notification-denied", principal: worker,
			notificationErr: errors.Join(workitems.ErrPermissionDenied, privateCause), wantStatus: http.StatusForbidden,
			want: mutationLogExpectation{actorID: worker.ID, operation: "notification_replay", outcome: "failure", notificationID: mutationNotificationID, status: http.StatusForbidden, errorCode: "PERMISSION_DENIED", level: "WARN"},
		},
		{
			name: "notification not replayable", method: http.MethodPost, path: "/v1/notifications/" + mutationNotificationID + "/replay",
			body: `{"reason":"` + mutationReplayReasonSecret + `"}`, requestID: "request-mutation-notification-conflict", principal: manager,
			notificationErr: errors.Join(notifications.ErrNotReplayable, privateCause), wantStatus: http.StatusConflict,
			want: mutationLogExpectation{actorID: manager.ID, operation: "notification_replay", outcome: "failure", notificationID: mutationNotificationID, status: http.StatusConflict, errorCode: "NOTIFICATION_NOT_RETRYABLE", level: "WARN"},
		},
		{
			name: "notification internal", method: http.MethodPost, path: "/v1/notifications/" + mutationNotificationID + "/replay",
			body: `{"reason":"` + mutationReplayReasonSecret + `"}`, requestID: "request-mutation-notification-internal", principal: manager,
			notificationErr: privateCause, wantStatus: http.StatusInternalServerError,
			want: mutationLogExpectation{actorID: manager.ID, operation: "notification_replay", outcome: "failure", notificationID: mutationNotificationID, status: http.StatusInternalServerError, errorCode: "INTERNAL_ERROR", level: "ERROR"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			server := newMutationLogTestServer(t, &output, test.principal,
				&mutationLogWorkItems{err: test.workErr},
				&mutationLogNotifications{err: test.notificationErr},
			)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+mutationTokenSentinel)
			request.Header.Set("X-Request-ID", test.requestID)
			request.Header.Set("Idempotency-Key", mutationIdempotencySecret)
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if test.ifMatch != "" {
				request.Header.Set("If-Match", test.ifMatch)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}

			test.want.requestID = test.requestID
			assertMutationLogOutput(t, output.String(), test.want)
		})
	}
}

func TestAPINonMutationAndAuthenticationFailuresDoNotEmitMutationLogs(t *testing.T) {
	manager := mutationLogPrincipal(mutationActorManagerID, auth.RoleManager)
	tests := []struct {
		name, method, path, token string
	}{
		{name: "authentication failure", method: http.MethodPost, path: "/v1/work-items", token: "not-valid"},
		{name: "wrong method", method: http.MethodPatch, path: "/v1/work-items", token: mutationTokenSentinel},
		{name: "unknown route", method: http.MethodPost, path: "/v1/not-a-route", token: mutationTokenSentinel},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			server := newMutationLogTestServer(t, &output, manager, &mutationLogWorkItems{}, &mutationLogNotifications{})
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{"title":"private"}`))
			request.Header.Set("Authorization", "Bearer "+test.token)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Request-ID", "request-non-mutation")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)

			records := parseMutationLogRecords(t, output.String())
			if got := recordsWithMessage(records, "api mutation"); len(got) != 0 {
				t.Fatalf("mutation records=%#v", got)
			}
			if got := recordsWithMessage(records, "http request"); len(got) != 1 {
				t.Fatalf("access records=%d output=%s", len(got), output.String())
			}
		})
	}
}

func TestMutationLogContractAssertionsFailClosed(t *testing.T) {
	falseValue := false
	want := mutationLogExpectation{
		requestID: "request-gate", actorID: mutationActorManagerID,
		operation: "create", outcome: "success", workItemID: mutationResultItemID,
		idempotent: &falseValue, level: "INFO",
	}
	baseline := map[string]any{
		"level": "INFO", "msg": "api mutation", "request_id": want.requestID,
		"actor_id": want.actorID, "operation": want.operation, "outcome": want.outcome,
		"work_item_id": want.workItemID, "idempotent": false,
	}
	if _, err := checkMutationLogRecords([]map[string]any{baseline}, want); err != nil {
		t.Fatalf("baseline rejected: %v", err)
	}

	mutations := map[string]func() []map[string]any{
		"missing request id":    func() []map[string]any { return mutationLogDeleteField(baseline, "request_id") },
		"missing actor id":      func() []map[string]any { return mutationLogDeleteField(baseline, "actor_id") },
		"missing operation":     func() []map[string]any { return mutationLogDeleteField(baseline, "operation") },
		"missing outcome":       func() []map[string]any { return mutationLogDeleteField(baseline, "outcome") },
		"missing resource id":   func() []map[string]any { return mutationLogDeleteField(baseline, "work_item_id") },
		"wrong operation":       func() []map[string]any { return mutationLogReplaceField(baseline, "operation", "assign") },
		"wrong outcome":         func() []map[string]any { return mutationLogReplaceField(baseline, "outcome", "ok") },
		"duplicate record":      func() []map[string]any { return []map[string]any{baseline, baseline} },
		"sensitive field added": func() []map[string]any { return mutationLogReplaceField(baseline, "title", mutationTitleSentinel) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, err := checkMutationLogRecords(mutate(), want); err == nil {
				t.Fatal("mutated record passed the contract assertion")
			}
		})
	}
}

func assertMutationLogOutput(t *testing.T, output string, want mutationLogExpectation) {
	t.Helper()
	records := parseMutationLogRecords(t, output)
	mutation, err := checkMutationLogRecords(records, want)
	if err != nil {
		t.Fatalf("mutation log contract: %v\noutput:\n%s", err, output)
	}
	access := recordsWithMessage(records, "http request")
	if len(access) != 1 || access[0]["request_id"] != want.requestID {
		t.Fatalf("access records=%#v output=%s", access, output)
	}
	encoded, err := json.Marshal(mutation)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range mutationPrivateSentinels {
		if bytes.Contains(encoded, []byte(sentinel)) {
			t.Fatalf("private sentinel %q leaked in mutation record: %s", sentinel, encoded)
		}
	}
}

func checkMutationLogRecords(records []map[string]any, want mutationLogExpectation) (map[string]any, error) {
	mutationRecords := recordsWithMessage(records, "api mutation")
	if len(mutationRecords) != 1 {
		return nil, fmt.Errorf("got %d mutation records, want 1", len(mutationRecords))
	}
	record := mutationRecords[0]
	wantValues := map[string]any{
		"level": want.level, "msg": "api mutation", "request_id": want.requestID,
		"actor_id": want.actorID, "operation": want.operation, "outcome": want.outcome,
	}
	if want.workItemID != "" {
		wantValues["work_item_id"] = want.workItemID
	}
	if want.notificationID != "" {
		wantValues["notification_id"] = want.notificationID
	}
	if want.idempotent != nil {
		wantValues["idempotent"] = *want.idempotent
	}
	if want.status != 0 {
		wantValues["status"] = float64(want.status)
	}
	if want.errorCode != "" {
		wantValues["error_code"] = want.errorCode
	}
	for key, value := range wantValues {
		if record[key] != value {
			return nil, fmt.Errorf("%s=%#v, want %#v", key, record[key], value)
		}
	}
	allowedKeys := []string{"time"}
	for key := range wantValues {
		allowedKeys = append(allowedKeys, key)
	}
	for key := range record {
		if !slices.Contains(allowedKeys, key) {
			return nil, fmt.Errorf("unexpected field %q", key)
		}
	}
	return record, nil
}

func parseMutationLogRecords(t *testing.T, output string) []map[string]any {
	t.Helper()
	var records []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode JSON log: %v line=%q", err, scanner.Text())
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func recordsWithMessage(records []map[string]any, message string) []map[string]any {
	var matches []map[string]any
	for _, record := range records {
		if record["msg"] == message {
			matches = append(matches, record)
		}
	}
	return matches
}

func mutationLogDeleteField(source map[string]any, key string) []map[string]any {
	clone := mutationLogClone(source)
	delete(clone, key)
	return []map[string]any{clone}
}

func mutationLogReplaceField(source map[string]any, key string, value any) []map[string]any {
	clone := mutationLogClone(source)
	clone[key] = value
	return []map[string]any{clone}
}

func mutationLogClone(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func mutationLogPrincipal(actorID string, role auth.Role) auth.Principal {
	return auth.Principal{ID: actorID, DisplayName: mutationNameSentinel, Email: mutationEmailSentinel, Role: role}
}

func newMutationLogTestServer(t *testing.T, output *bytes.Buffer, principal auth.Principal, work WorkItemApplication, notification NotificationApplication) http.Handler {
	t.Helper()
	server, err := NewServer(ServerConfig{
		Logger:        slog.New(slog.NewJSONHandler(output, nil)),
		Authenticator: mutationLogAuthenticator{principal: principal},
		WorkItems:     work, Notifications: notification,
		RequestID: func() (string, error) { return "request-generated", nil }, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

type mutationLogAuthenticator struct{ principal auth.Principal }

func (authenticator mutationLogAuthenticator) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	if token != mutationTokenSentinel {
		return auth.Principal{}, auth.ErrInvalidCredentials
	}
	return authenticator.principal, nil
}

type mutationLogWorkItems struct {
	result workitems.MutationResult
	err    error
}

func (application *mutationLogWorkItems) Create(context.Context, auth.Principal, workitems.CreateInput) (workitems.MutationResult, error) {
	return application.result, application.err
}
func (application *mutationLogWorkItems) Get(context.Context, auth.Principal, string) (workitems.WorkItem, error) {
	return workitems.WorkItem{}, application.err
}
func (application *mutationLogWorkItems) ListManager(context.Context, auth.Principal, workitems.ListInput) (workitems.Page, error) {
	return workitems.Page{}, application.err
}
func (application *mutationLogWorkItems) ListOwn(context.Context, auth.Principal, workitems.ListInput) (workitems.Page, error) {
	return workitems.Page{}, application.err
}
func (application *mutationLogWorkItems) Assign(context.Context, auth.Principal, string, workitems.AssignmentInput) (workitems.MutationResult, error) {
	return application.result, application.err
}
func (application *mutationLogWorkItems) Start(context.Context, auth.Principal, string, workitems.TransitionInput) (workitems.MutationResult, error) {
	return application.result, application.err
}
func (application *mutationLogWorkItems) Complete(context.Context, auth.Principal, string, workitems.TransitionInput) (workitems.MutationResult, error) {
	return application.result, application.err
}
func (application *mutationLogWorkItems) History(context.Context, auth.Principal, string, workitems.HistoryInput) (workitems.EventPage, error) {
	return workitems.EventPage{}, application.err
}

type mutationLogNotifications struct {
	result notifications.Delivery
	err    error
}

func (application *mutationLogNotifications) List(context.Context, auth.Principal, notifications.ListInput) (notifications.Page, error) {
	return notifications.Page{}, application.err
}
func (application *mutationLogNotifications) Get(context.Context, auth.Principal, string) (notifications.Delivery, error) {
	return application.result, application.err
}
func (application *mutationLogNotifications) Replay(context.Context, auth.Principal, string, notifications.ReplayInput) (notifications.Delivery, error) {
	return application.result, application.err
}
