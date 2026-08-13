package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/notifications"
	"github.com/eidrogo/work-management-api/internal/platform/clock"
	"github.com/eidrogo/work-management-api/internal/platform/id"
	"github.com/eidrogo/work-management-api/internal/seed"
	"github.com/eidrogo/work-management-api/internal/testsupport/postgrestest"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

func TestHTTPManagerWorkerLifecycleAndNotificationContract(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	authService := auth.NewService(auth.PostgresTokenStore{DB: db}, clock.Fixed{Time: now})
	itemService, _ := workitems.NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	store, _ := notifications.NewStore(db)
	notificationService, _ := notifications.NewService(store)
	handler, err := NewServer(ServerConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authenticator: authService, WorkItems: itemService, Notifications: notificationService, RequestID: id.NewUUID, MaxBodyBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	createBody := []byte(`{"title":"Inspect loading dock","description":"Verify HTTP lifecycle"}`)
	createdResponse := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items", seed.ManagerToken, map[string]string{"Idempotency-Key": "http-flow-create"}, createBody)
	assertHTTPResponse(t, createdResponse, http.StatusCreated, `"1"`, "")
	var created struct {
		Data workitems.WorkItem `json:"data"`
	}
	decodeBody(t, createdResponse, &created)
	if created.Data.Status != workitems.StatusUnassigned || created.Data.Version != 1 || created.Data.CreatorID != seed.ManagerID {
		t.Fatalf("created=%#v", created.Data)
	}
	replayResponse := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items", seed.ManagerToken, map[string]string{"Idempotency-Key": "http-flow-create"}, createBody)
	assertHTTPResponse(t, replayResponse, http.StatusOK, `"1"`, "true")
	var replayed struct {
		Data workitems.WorkItem `json:"data"`
	}
	decodeBody(t, replayResponse, &replayed)
	if !reflect.DeepEqual(created.Data, replayed.Data) {
		t.Fatalf("created=%#v replayed=%#v", created.Data, replayed.Data)
	}

	managerList := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/work-items", seed.ManagerToken, nil, nil)
	assertHTTPResponse(t, managerList, http.StatusOK, "", "")
	var listed struct {
		Data []workitems.WorkItem `json:"data"`
	}
	decodeBody(t, managerList, &listed)
	if !containsWorkItem(listed.Data, created.Data.ID) {
		t.Fatalf("manager list=%#v", listed.Data)
	}
	managerGet := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/work-items/"+created.Data.ID, seed.ManagerToken, nil, nil)
	assertHTTPResponse(t, managerGet, http.StatusOK, `"1"`, "")
	_ = managerGet.Body.Close()

	assignedResponse := doJSON(t, server.Client(), http.MethodPut, server.URL+"/v1/work-items/"+created.Data.ID+"/assignment", seed.ManagerToken, map[string]string{"If-Match": `"1"`}, []byte(`{"assignee_id":"`+seed.WorkerAliceID+`"}`))
	assertHTTPResponse(t, assignedResponse, http.StatusOK, `"2"`, "")
	var assigned struct {
		Data workitems.WorkItem `json:"data"`
	}
	decodeBody(t, assignedResponse, &assigned)
	if assigned.Data.Status != workitems.StatusAssigned || assigned.Data.AssigneeID != seed.WorkerAliceID || assigned.Data.Version != 2 {
		t.Fatalf("assigned=%#v", assigned.Data)
	}

	managerStart := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items/"+created.Data.ID+"/start", seed.ManagerToken, map[string]string{"If-Match": `"2"`}, nil)
	assertHTTPError(t, managerStart, http.StatusForbidden, "PERMISSION_DENIED")
	otherResponse := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/work-items/"+created.Data.ID, seed.WorkerBobToken, nil, nil)
	assertHTTPError(t, otherResponse, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	otherStart := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items/"+created.Data.ID+"/start", seed.WorkerBobToken, map[string]string{"If-Match": `"2"`}, nil)
	assertHTTPError(t, otherStart, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	otherQueue := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/me/work-items", seed.WorkerBobToken, nil, nil)
	assertHTTPResponse(t, otherQueue, http.StatusOK, "", "")
	var bobItems struct {
		Data []workitems.WorkItem `json:"data"`
	}
	decodeBody(t, otherQueue, &bobItems)
	if containsWorkItem(bobItems.Data, created.Data.ID) {
		t.Fatalf("Bob queue disclosed item: %#v", bobItems.Data)
	}

	aliceQueue := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/me/work-items?status=ASSIGNED", seed.WorkerAliceToken, nil, nil)
	assertHTTPResponse(t, aliceQueue, http.StatusOK, "", "")
	var aliceItems struct {
		Data []workitems.WorkItem `json:"data"`
	}
	decodeBody(t, aliceQueue, &aliceItems)
	if !containsWorkItem(aliceItems.Data, created.Data.ID) {
		t.Fatalf("Alice queue=%#v", aliceItems.Data)
	}
	startedResponse := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items/"+created.Data.ID+"/start", seed.WorkerAliceToken, map[string]string{"If-Match": `"2"`}, nil)
	assertHTTPResponse(t, startedResponse, http.StatusOK, `"3"`, "")
	_ = startedResponse.Body.Close()
	startReplay := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items/"+created.Data.ID+"/start", seed.WorkerAliceToken, nil, nil)
	assertHTTPResponse(t, startReplay, http.StatusOK, `"3"`, "true")
	_ = startReplay.Body.Close()
	completedResponse := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items/"+created.Data.ID+"/complete", seed.WorkerAliceToken, map[string]string{"If-Match": `"3"`}, nil)
	assertHTTPResponse(t, completedResponse, http.StatusOK, `"4"`, "")
	_ = completedResponse.Body.Close()

	historyResponse := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/work-items/"+created.Data.ID+"/history", seed.WorkerAliceToken, nil, nil)
	assertHTTPResponse(t, historyResponse, http.StatusOK, "", "")
	var history struct {
		Data []workitems.Event `json:"data"`
	}
	decodeBody(t, historyResponse, &history)
	wantTypes := []workitems.EventType{workitems.EventCreated, workitems.EventAssigned, workitems.EventMarkedInProgress, workitems.EventCompleted}
	if len(history.Data) != len(wantTypes) {
		t.Fatalf("history=%#v", history.Data)
	}
	for index, want := range wantTypes {
		if history.Data[index].Type != want {
			t.Fatalf("history[%d]=%#v want=%s", index, history.Data[index], want)
		}
		wantActor := seed.ManagerID
		if index >= 2 {
			wantActor = seed.WorkerAliceID
		}
		if history.Data[index].ActorID != wantActor {
			t.Fatalf("history[%d].actor=%q want=%q", index, history.Data[index].ActorID, wantActor)
		}
	}
	if history.Data[1].NewAssigneeID != seed.WorkerAliceID {
		t.Fatalf("assignment history=%#v", history.Data[1])
	}

	notificationList := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/notifications?work_item_id="+created.Data.ID, seed.ManagerToken, nil, nil)
	assertHTTPResponse(t, notificationList, http.StatusOK, "", "")
	var deliveries struct {
		Data []notifications.Delivery `json:"data"`
	}
	decodeBody(t, notificationList, &deliveries)
	if len(deliveries.Data) != 1 || deliveries.Data[0].WorkItemID != created.Data.ID || deliveries.Data[0].Status != notifications.StatusPending {
		t.Fatalf("deliveries=%#v", deliveries.Data)
	}
	notificationGet := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/notifications/"+deliveries.Data[0].ID, seed.ManagerToken, nil, nil)
	assertHTTPResponse(t, notificationGet, http.StatusOK, "", "")
	var detail struct {
		Data notifications.Delivery `json:"data"`
	}
	decodeBody(t, notificationGet, &detail)
	if detail.Data.RecipientEmail != "alice.worker@example.test" || detail.Data.MessageID == "" {
		t.Fatalf("notification detail=%#v", detail.Data)
	}
	workerNotificationList := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/notifications", seed.WorkerAliceToken, nil, nil)
	assertHTTPError(t, workerNotificationList, http.StatusForbidden, "PERMISSION_DENIED")
	workerNotificationGet := doJSON(t, server.Client(), http.MethodGet, server.URL+"/v1/notifications/"+deliveries.Data[0].ID, seed.WorkerAliceToken, nil, nil)
	assertHTTPError(t, workerNotificationGet, http.StatusForbidden, "PERMISSION_DENIED")
	workerReplay := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/notifications/"+deliveries.Data[0].ID+"/replay", seed.WorkerAliceToken, nil, []byte(`{"reason":"not allowed"}`))
	assertHTTPError(t, workerReplay, http.StatusForbidden, "PERMISSION_DENIED")
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_item_events WHERE work_item_id=$1`, created.Data.ID); got != 4 {
		t.Fatalf("physical event count=%d", got)
	}
}

func TestCreateIdempotencyHTTPReturnsOriginalETagAndBody(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	authService := auth.NewService(auth.PostgresTokenStore{DB: db}, clock.Fixed{Time: now})
	itemService, _ := workitems.NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	store, _ := notifications.NewStore(db)
	notificationService, _ := notifications.NewService(store)
	handler, err := NewServer(ServerConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authenticator: authService, WorkItems: itemService, Notifications: notificationService, RequestID: id.NewUUID, MaxBodyBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	createBody := []byte(`{"title":"Immutable HTTP create","description":"Original"}`)
	createdResponse := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items", seed.ManagerToken, map[string]string{"Idempotency-Key": "http-immutable-create"}, createBody)
	if createdResponse.StatusCode != http.StatusCreated || createdResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("create status=%d etag=%q", createdResponse.StatusCode, createdResponse.Header.Get("ETag"))
	}
	var created struct {
		Data workitems.WorkItem `json:"data"`
	}
	decodeBody(t, createdResponse, &created)
	assignedResponse := doJSON(t, server.Client(), http.MethodPut, server.URL+"/v1/work-items/"+created.Data.ID+"/assignment", seed.ManagerToken, map[string]string{"If-Match": `"1"`}, []byte(`{"assignee_id":"`+seed.WorkerAliceID+`"}`))
	if assignedResponse.StatusCode != http.StatusOK {
		t.Fatalf("assign status=%d", assignedResponse.StatusCode)
	}
	_ = assignedResponse.Body.Close()
	startedResponse := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items/"+created.Data.ID+"/start", seed.WorkerAliceToken, map[string]string{"If-Match": `"2"`}, nil)
	if startedResponse.StatusCode != http.StatusOK {
		t.Fatalf("start status=%d", startedResponse.StatusCode)
	}
	_ = startedResponse.Body.Close()
	completedResponse := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items/"+created.Data.ID+"/complete", seed.WorkerAliceToken, map[string]string{"If-Match": `"3"`}, nil)
	if completedResponse.StatusCode != http.StatusOK {
		t.Fatalf("complete status=%d", completedResponse.StatusCode)
	}
	_ = completedResponse.Body.Close()
	replayResponse := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items", seed.ManagerToken, map[string]string{"Idempotency-Key": "http-immutable-create"}, createBody)
	if replayResponse.StatusCode != http.StatusOK || replayResponse.Header.Get("Idempotent-Replayed") != "true" || replayResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("replay status=%d idempotent=%q etag=%q", replayResponse.StatusCode, replayResponse.Header.Get("Idempotent-Replayed"), replayResponse.Header.Get("ETag"))
	}
	var replayed struct {
		Data workitems.WorkItem `json:"data"`
	}
	decodeBody(t, replayResponse, &replayed)
	if !reflect.DeepEqual(replayed.Data, created.Data) {
		t.Fatalf("created=%#v replayed=%#v", created.Data, replayed.Data)
	}
}

func TestCreateRejectsHeaderNewlineTitleWithoutPersistence(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	authService := auth.NewService(auth.PostgresTokenStore{DB: db}, clock.Fixed{Time: now})
	itemService, _ := workitems.NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	store, _ := notifications.NewStore(db)
	notificationService, _ := notifications.NewService(store)
	handler, err := NewServer(ServerConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authenticator: authService, WorkItems: itemService, Notifications: notificationService, RequestID: id.NewUUID, MaxBodyBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	beforeItems := postgrestest.Count(t, db, `SELECT count(*) FROM work_items`)
	beforeEvents := postgrestest.Count(t, db, `SELECT count(*) FROM work_item_events`)
	response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/work-items", seed.ManagerToken,
		map[string]string{"Idempotency-Key": "rejected-header-title"}, []byte(`{"title":"Quarterly handoff\nURGENT"}`))
	assertHTTPError(t, response, http.StatusBadRequest, "INVALID_INPUT")
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_items`); got != beforeItems {
		t.Fatalf("work items=%d want=%d", got, beforeItems)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM work_item_events`); got != beforeEvents {
		t.Fatalf("events=%d want=%d", got, beforeEvents)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM idempotency_keys WHERE idempotency_key='rejected-header-title'`); got != 0 {
		t.Fatalf("idempotency records=%d", got)
	}
	if got := postgrestest.Count(t, db, `SELECT count(*) FROM notification_deliveries`); got != 0 {
		t.Fatalf("notifications=%d", got)
	}
}

func TestReplayHTTPPersistsExactRequestIDAndAuthenticatedActor(t *testing.T) {
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	_, _ = seed.Apply(context.Background(), db, clock.Fixed{Time: now})
	authService := auth.NewService(auth.PostgresTokenStore{DB: db}, clock.Fixed{Time: now})
	itemService, _ := workitems.NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	store, _ := notifications.NewStore(db)
	notificationService, _ := notifications.NewService(store)
	handler, err := NewServer(ServerConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authenticator: authService, WorkItems: itemService, Notifications: notificationService, RequestID: id.NewUUID, MaxBodyBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	created, err := itemService.Create(context.Background(), auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya"}, workitems.CreateInput{Title: "Replay HTTP", IdempotencyKey: "replay-http-create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = itemService.Assign(context.Background(), auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya"}, created.Item.ID, workitems.AssignmentInput{AssigneeID: seed.WorkerAliceID, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	delivery, found, err := store.ClaimNext(context.Background(), notifications.ClaimRequest{WorkerID: "worker-http", LeaseDuration: time.Minute, MaxAttempts: 3})
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	if err = store.Complete(context.Background(), notifications.Completion{DeliveryID: delivery.ID, WorkerID: "worker-http", LeaseToken: delivery.LeaseToken, AttemptNumber: delivery.AttemptCount, Outcome: notifications.OutcomeFailed, FailureCode: notifications.FailureSMTPRecipientRejected}); err != nil {
		t.Fatal(err)
	}
	requestID := "request-http-persisted"
	response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/v1/notifications/"+delivery.ID+"/replay", seed.ManagerToken, map[string]string{"X-Request-ID": requestID}, []byte(`{"reason":"  operator verified recipient  "}`))
	if response.StatusCode != http.StatusAccepted || response.Header.Get("X-Request-ID") != requestID {
		t.Fatalf("status=%d request_id=%q", response.StatusCode, response.Header.Get("X-Request-ID"))
	}
	_ = response.Body.Close()
	var actorID, storedRequestID, channel, reason string
	if err = db.QueryRowContext(context.Background(), `SELECT actor_id,request_id,channel,reason FROM notification_replay_events WHERE notification_id=$1`, delivery.ID).Scan(&actorID, &storedRequestID, &channel, &reason); err != nil {
		t.Fatal(err)
	}
	if actorID != seed.ManagerID || storedRequestID != requestID || channel != string(notifications.ReplayChannelHTTP) || reason != "operator verified recipient" {
		t.Fatalf("actor=%q request=%q channel=%q reason=%q", actorID, storedRequestID, channel, reason)
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
	defer func() { _ = response.Body.Close() }()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func assertHTTPResponse(t *testing.T, response *http.Response, status int, etag, replayed string) {
	t.Helper()
	if response.StatusCode != status {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("status=%d want=%d body=%s", response.StatusCode, status, body)
	}
	if requestID := response.Header.Get("X-Request-ID"); requestID == "" {
		t.Fatal("X-Request-ID is blank")
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type=%q", contentType)
	}
	if got := response.Header.Get("ETag"); got != etag {
		t.Fatalf("ETag=%q want=%q", got, etag)
	}
	if got := response.Header.Get("Idempotent-Replayed"); got != replayed {
		t.Fatalf("Idempotent-Replayed=%q want=%q", got, replayed)
	}
}

func assertHTTPError(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	assertHTTPResponse(t, response, status, "", "")
	requestID := response.Header.Get("X-Request-ID")
	var body errorEnvelope
	decodeBody(t, response, &body)
	if body.Error.Code != code || body.Error.RequestID != requestID {
		t.Fatalf("error=%#v header request id=%q", body.Error, requestID)
	}
}

func containsWorkItem(items []workitems.WorkItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
