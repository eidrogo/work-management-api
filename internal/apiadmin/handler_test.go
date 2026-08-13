package apiadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdminHandlerServesOperationalEndpoints(t *testing.T) {
	var readinessCalls atomic.Int32
	handler, err := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Readiness: func(context.Context) error {
			readinessCalls.Add(1)
			return nil
		},
		Metrics: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "metrics sentinel\n")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, wantStatus := range map[string]string{
		"/health/live":  "ok",
		"/health/ready": "ready",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, response.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["status"] != wantStatus {
			t.Fatalf("%s body=%v", path, body)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || response.Body.String() != "metrics sentinel\n" {
		t.Fatalf("metrics status=%d body=%q", response.Code, response.Body.String())
	}
	if readinessCalls.Load() != 1 {
		t.Fatalf("readiness calls=%d", readinessCalls.Load())
	}
}

func TestAdminHandlerReadinessFailure(t *testing.T) {
	var readinessCalls atomic.Int32
	var logs bytes.Buffer
	handler, err := New(Config{
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Readiness: func(context.Context) error {
			readinessCalls.Add(1)
			return errors.New("sensitive database detail")
		},
		Metrics: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"status\":\"not ready\"}\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sensitive") || readinessCalls.Load() != 1 {
		t.Fatalf("body=%q readiness calls=%d", response.Body.String(), readinessCalls.Load())
	}
	if !strings.Contains(logs.String(), "sensitive database detail") {
		t.Fatalf("log=%q", logs.String())
	}
}

func TestAdminHandlerReadinessPropagatesTwoSecondDeadline(t *testing.T) {
	deadlineSeen := make(chan time.Duration, 1)
	handler, err := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Readiness: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				return errors.New("readiness context has no deadline")
			}
			deadlineSeen <- time.Until(deadline)
			<-ctx.Done()
			return ctx.Err()
		},
		Metrics: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	parent, cancel := context.WithCancel(request.Context())
	request = request.WithContext(parent)
	result := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		result <- response.Code
	}()
	var remaining time.Duration
	select {
	case remaining = <-deadlineSeen:
	case <-time.After(time.Second):
		t.Fatal("readiness did not receive context")
	}
	if remaining < 1500*time.Millisecond || remaining > 2100*time.Millisecond {
		t.Fatalf("readiness deadline remaining=%s", remaining)
	}
	cancel()
	select {
	case status := <-result:
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status=%d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness did not observe cancellation")
	}
}

func TestAdminHandlerRejectsNonGETHealthMethods(t *testing.T) {
	handler := newAdminTestHandler(t)
	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
}

func TestAdminHandlerDoesNotServeBusinessRoutes(t *testing.T) {
	handler := newAdminTestHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/work-items", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestAdminHandlerRequiresDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	readiness := func(context.Context) error { return nil }
	metrics := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, config := range []Config{
		{Readiness: readiness, Metrics: metrics},
		{Logger: logger, Metrics: metrics},
		{Logger: logger, Readiness: readiness},
	} {
		if handler, err := New(config); handler != nil || err == nil || err.Error() != "api admin dependencies are required" {
			t.Fatalf("handler=%v error=%v", handler, err)
		}
	}
}

func newAdminTestHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := New(Config{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Readiness: func(context.Context) error { return nil },
		Metrics:   http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
