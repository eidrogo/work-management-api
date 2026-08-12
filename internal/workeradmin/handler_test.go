package workeradmin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthyEndpoints(t *testing.T) {
	handler, err := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Readiness: func(context.Context) error { return nil }, Metrics: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s=%d", path, response.Code)
		}
	}
}
