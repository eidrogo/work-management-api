package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
)

const (
	noncanonicalItemID         = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	noncanonicalNotificationID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	noncanonicalRequestID      = "request-noncanonical-path"
)

var noncanonicalMutationPaths = []struct {
	name, method, path, body string
}{
	{name: "create repeated slash", method: http.MethodPost, path: "/v1//work-items", body: `{"title":"Inspect"}`},
	{name: "create dot segment", method: http.MethodPost, path: "/v1/./work-items", body: `{"title":"Inspect"}`},
	{name: "create dot-dot segment", method: http.MethodPost, path: "/v1/ignored/../work-items", body: `{"title":"Inspect"}`},
	{name: "create encoded collection", method: http.MethodPost, path: "/v1/%77ork-items", body: `{"title":"Inspect"}`},
	{name: "create encoded dot segment", method: http.MethodPost, path: "/v1/%2e/work-items", body: `{"title":"Inspect"}`},
	{name: "assignment repeated slash", method: http.MethodPut, path: "/v1/work-items//" + noncanonicalItemID + "/assignment", body: `{"assignee_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc"}`},
	{name: "assignment dot-dot segment", method: http.MethodPut, path: "/v1/work-items/" + noncanonicalItemID + "/ignored/../assignment", body: `{"assignee_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc"}`},
	{name: "assignment encoded action", method: http.MethodPut, path: "/v1/work-items/" + noncanonicalItemID + "/%61ssignment", body: `{"assignee_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc"}`},
	{name: "start dot segment", method: http.MethodPost, path: "/v1/work-items/" + noncanonicalItemID + "/./start"},
	{name: "start encoded ID", method: http.MethodPost, path: "/v1/work-items/%61aaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/start"},
	{name: "complete repeated slash", method: http.MethodPost, path: "/v1/work-items/" + noncanonicalItemID + "//complete"},
	{name: "notification replay repeated slash", method: http.MethodPost, path: "/v1/notifications//" + noncanonicalNotificationID + "/replay", body: `{"reason":"retry"}`},
	{name: "notification replay dot segment", method: http.MethodPost, path: "/v1/notifications/" + noncanonicalNotificationID + "/./replay", body: `{"reason":"retry"}`},
	{name: "notification replay dot-dot segment", method: http.MethodPost, path: "/v1/notifications/" + noncanonicalNotificationID + "/ignored/../replay", body: `{"reason":"retry"}`},
	{name: "notification replay encoded action", method: http.MethodPost, path: "/v1/notifications/" + noncanonicalNotificationID + "/%72eplay", body: `{"reason":"retry"}`},
	{name: "notification replay encoded slash", method: http.MethodPost, path: "/v1/notifications/" + noncanonicalNotificationID + "%2freplay", body: `{"reason":"retry"}`},
}

var noncanonicalPublicPaths = []struct {
	name, method, path string
}{
	{name: "bare API prefix", method: http.MethodGet, path: "/v1"},
	{name: "leading repeated slash", method: http.MethodPost, path: "//v1/work-items"},
}

func TestNoncanonicalPathPreservesAccessAndMutationLoggingAndMetrics(t *testing.T) {
	tests := []struct {
		name, path                   string
		authenticated                bool
		wantStatus, wantAuthFailures int
	}{
		{name: "authenticated encoded path", path: "/v1/work-items/" + noncanonicalItemID + "/%73tart", authenticated: true, wantStatus: http.StatusNotFound},
		{name: "unauthenticated repeated slash", path: "/v1/work-items/" + noncanonicalItemID + "//start", wantStatus: http.StatusUnauthorized, wantAuthFailures: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			metrics := &noncanonicalMetricRecorder{}
			work := &recordingWorkItems{}
			notification := &recordingNotifications{}
			server, err := NewServer(ServerConfig{
				Logger:        slog.New(slog.NewJSONHandler(&output, nil)),
				Authenticator: replayAuthenticator{principals: map[string]auth.Principal{"valid": {ID: "manager", Role: auth.RoleManager}}},
				WorkItems:     work, Notifications: notification,
				RequestID:    func() (string, error) { return "request-generated", nil },
				MaxBodyBytes: 1024, MetricRecorder: metrics,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := newNoncanonicalMutationRequest(t, "", http.MethodPost, test.path, "", test.authenticated)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			assertNoncanonicalPathResponse(t, response.Result(), noncanonicalRequestID, test.wantStatus, map[int]string{
				http.StatusNotFound: "RESOURCE_NOT_FOUND", http.StatusUnauthorized: "AUTHENTICATION_REQUIRED",
			}[test.wantStatus])

			records := parseMutationLogRecords(t, output.String())
			access := recordsWithMessage(records, "http request")
			if len(access) != 1 || access[0]["request_id"] != noncanonicalRequestID || access[0]["status"] != float64(test.wantStatus) {
				t.Fatalf("access records=%#v", access)
			}
			if mutation := recordsWithMessage(records, "api mutation"); len(mutation) != 0 {
				t.Fatalf("mutation records=%#v", mutation)
			}
			if len(metrics.httpCalls) != 1 || metrics.httpCalls[0].method != http.MethodPost || metrics.httpCalls[0].route != "/unknown" || metrics.httpCalls[0].status != test.wantStatus {
				t.Fatalf("HTTP metrics=%#v", metrics.httpCalls)
			}
			if metrics.authFailures != test.wantAuthFailures || metrics.mutations != 0 {
				t.Fatalf("auth failures=%d mutations=%d", metrics.authFailures, metrics.mutations)
			}
			assertNoMutationApplicationCall(t, work, notification)
		})
	}
}

func TestHTTPRejectsNoncanonicalMutationPathsWithoutRedirectOrApplicationCall(t *testing.T) {
	for _, test := range noncanonicalMutationPaths {
		t.Run(test.name, func(t *testing.T) {
			work := &recordingWorkItems{}
			notification := &recordingNotifications{}
			server := newRecordingServer(t, work, notification)
			request := newNoncanonicalMutationRequest(t, "", test.method, test.path, test.body, true)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)

			assertNoncanonicalPathResponse(t, response.Result(), noncanonicalRequestID, http.StatusNotFound, "RESOURCE_NOT_FOUND")
			assertNoMutationApplicationCall(t, work, notification)
		})
	}
}

func TestDefaultHTTPClientCannotFollowNoncanonicalMutationPathIntoApplication(t *testing.T) {
	for _, test := range noncanonicalMutationPaths {
		t.Run(test.name, func(t *testing.T) {
			work := &recordingWorkItems{}
			notification := &recordingNotifications{}
			server := httptest.NewServer(newRecordingServer(t, work, notification))
			defer server.Close()

			request := newNoncanonicalMutationRequest(t, server.URL, test.method, test.path, test.body, true)
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = response.Body.Close() })

			assertNoncanonicalPathResponse(t, response, noncanonicalRequestID, http.StatusNotFound, "RESOURCE_NOT_FOUND")
			if response.Request.URL.EscapedPath() != test.path {
				t.Fatalf("client followed or rewrote request path: got=%q want=%q", response.Request.URL.EscapedPath(), test.path)
			}
			assertNoMutationApplicationCall(t, work, notification)
		})
	}
}

func TestNoncanonicalV1PathsPreserveAuthenticationPrecedence(t *testing.T) {
	tests := append([]struct {
		name, method, path, body string
	}{}, noncanonicalMutationPaths...)
	tests = append(tests, struct {
		name, method, path, body string
	}{name: "encoded API prefix", method: http.MethodPost, path: "/%76%31/work-items", body: `{"title":"Inspect"}`})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Run("direct handler", func(t *testing.T) {
				work := &recordingWorkItems{}
				notification := &recordingNotifications{}
				server := newRecordingServer(t, work, notification)
				request := newNoncanonicalMutationRequest(t, "", test.method, test.path, test.body, false)
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)

				assertNoncanonicalPathResponse(t, response.Result(), noncanonicalRequestID, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
				if response.Header().Get("WWW-Authenticate") == "" {
					t.Fatal("missing WWW-Authenticate header")
				}
				assertNoMutationApplicationCall(t, work, notification)
			})

			t.Run("default HTTP client", func(t *testing.T) {
				work := &recordingWorkItems{}
				notification := &recordingNotifications{}
				server := httptest.NewServer(newRecordingServer(t, work, notification))
				defer server.Close()

				request := newNoncanonicalMutationRequest(t, server.URL, test.method, test.path, test.body, false)
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = response.Body.Close() })

				assertNoncanonicalPathResponse(t, response, noncanonicalRequestID, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
				if response.Header.Get("WWW-Authenticate") == "" {
					t.Fatal("missing WWW-Authenticate header")
				}
				if response.Request.URL.EscapedPath() != test.path {
					t.Fatalf("client followed or rewrote request path: got=%q want=%q", response.Request.URL.EscapedPath(), test.path)
				}
				assertNoMutationApplicationCall(t, work, notification)
			})
		})
	}
}

func TestNoncanonicalPublicPrefixesReturnStableNotFoundWithoutRedirect(t *testing.T) {
	for _, test := range noncanonicalPublicPaths {
		t.Run(test.name, func(t *testing.T) {
			t.Run("direct handler", func(t *testing.T) {
				work := &recordingWorkItems{}
				notification := &recordingNotifications{}
				server := newRecordingServer(t, work, notification)
				request := newNoncanonicalMutationRequest(t, "", test.method, test.path, "", false)
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)

				assertNoncanonicalPathResponse(t, response.Result(), noncanonicalRequestID, http.StatusNotFound, "RESOURCE_NOT_FOUND")
				assertNoMutationApplicationCall(t, work, notification)
			})

			t.Run("default HTTP client", func(t *testing.T) {
				work := &recordingWorkItems{}
				notification := &recordingNotifications{}
				server := httptest.NewServer(newRecordingServer(t, work, notification))
				defer server.Close()

				request := newNoncanonicalMutationRequest(t, server.URL, test.method, test.path, "", false)
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = response.Body.Close() })

				assertNoncanonicalPathResponse(t, response, noncanonicalRequestID, http.StatusNotFound, "RESOURCE_NOT_FOUND")
				if response.Request.URL.EscapedPath() != test.path {
					t.Fatalf("client followed or rewrote request path: got=%q want=%q", response.Request.URL.EscapedPath(), test.path)
				}
				assertNoMutationApplicationCall(t, work, notification)
			})
		})
	}
}

func newNoncanonicalMutationRequest(t *testing.T, baseURL, method, path, body string, authenticated bool) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request-ID", noncanonicalRequestID)
	if authenticated {
		request.Header.Set("Authorization", "Bearer valid")
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.Contains(path, "/assignment") || strings.Contains(path, "/start") || strings.Contains(path, "/complete") || strings.Contains(path, "%61ssignment") {
		request.Header.Set("If-Match", `"1"`)
	}
	return request
}

func assertNoncanonicalPathResponse(t *testing.T, response *http.Response, requestID string, status int, code string) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status=%d Location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if location := response.Header.Get("Location"); location != "" {
		t.Fatalf("Location=%q", location)
	}
	if response.Header.Get("X-Request-ID") != requestID {
		t.Fatalf("X-Request-ID=%q", response.Header.Get("X-Request-ID"))
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("Content-Type=%q", response.Header.Get("Content-Type"))
	}
	var envelope errorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != code || envelope.Error.RequestID != requestID {
		t.Fatalf("error=%#v", envelope.Error)
	}
}

func assertNoMutationApplicationCall(t *testing.T, work *recordingWorkItems, notification *recordingNotifications) {
	t.Helper()
	if work.call != "" || notification.call != "" {
		t.Fatalf("unexpected application calls: work=%q notification=%q", work.call, notification.call)
	}
}

type noncanonicalHTTPMetric struct {
	method, route string
	status        int
}

type noncanonicalMetricRecorder struct {
	httpCalls    []noncanonicalHTTPMetric
	authFailures int
	mutations    int
}

func (recorder *noncanonicalMetricRecorder) RecordHTTP(method, route string, status int, _ time.Duration) {
	recorder.httpCalls = append(recorder.httpCalls, noncanonicalHTTPMetric{method: method, route: route, status: status})
}
func (recorder *noncanonicalMetricRecorder) RecordAuthFailure()            { recorder.authFailures++ }
func (*noncanonicalMetricRecorder) RecordAuthorizationFailure()            {}
func (recorder *noncanonicalMetricRecorder) RecordMutation(string, string) { recorder.mutations++ }
