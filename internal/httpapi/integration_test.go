package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/notifications"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/platform/database"
	"github.com/eidrogo/work-management-api/internal/platform/id"
	"github.com/eidrogo/work-management-api/internal/seed"
	"github.com/eidrogo/work-management-api/internal/testsupport/postgrestest"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

func TestDocumentedHTTPFlow(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	authService := auth.NewService(auth.PostgresTokenStore{DB: db}, clock.Fixed{Time: now})
	itemService, _ := workitems.NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	store, _ := notifications.NewStore(db)
	notificationService, _ := notifications.NewService(store, clock.Fixed{Time: now})
	handler, err := NewServer(ServerConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authenticator: authService, WorkItems: itemService, Notifications: notificationService, Readiness: func(ctx context.Context) error { return database.Ready(ctx, db) }, RequestID: id.NewUUID, MaxBodyBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	createdResponse := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items", seed.ManagerToken, map[string]string{"Idempotency-Key": "http-flow-create"}, []byte(`{"title":"Inspect loading dock"}`))
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", createdResponse.StatusCode)
	}
	var created struct {
		Data workitems.WorkItem `json:"data"`
	}
	decodeBody(t, createdResponse, &created)
	assignedResponse := doJSON(t, server.Client(), http.MethodPut, server.URL+"/v1/work-items/"+created.Data.ID+"/assignment", seed.ManagerToken, map[string]string{"If-Match": `"1"`}, []byte(`{"assignee_id":"`+seed.WorkerAliceID+`"}`))
	if assignedResponse.StatusCode != http.StatusOK {
		t.Fatalf("assign status=%d", assignedResponse.StatusCode)
	}
	otherResponse := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/work-items/"+created.Data.ID, seed.WorkerBobToken, nil, nil)
	if otherResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("other worker status=%d", otherResponse.StatusCode)
	}
}

func doJSON(t *testing.T, client *http.Client, method, url, token string, headers map[string]string, body []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func decodeBody(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}
