package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
	metricspkg "github.com/eidrogo/work-management-api/internal/metrics"
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

func TestHTTPRejectsNULPersistedTextWithoutSideEffects(t *testing.T) {
	for _, test := range []struct {
		name, field, message, body, idempotencyKey string
	}{
		{name: "title", field: "title", message: "Title contains an unsupported character.", body: `{"title":"Quarterly\u0000 handoff","description":"Inspect loading dock"}`, idempotencyKey: "nul-http-title"},
		{name: "description", field: "description", message: "Description contains an unsupported character.", body: `{"title":"Quarterly handoff","description":"Inspect\u0000 loading dock"}`, idempotencyKey: "nul-http-description"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			fixture := newLiveHTTPFixture(t)
			beforeItems := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_items`)
			beforeEvents := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_item_events`)
			beforeNotifications := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM notification_deliveries`)
			requestID := "request-http-nul-" + test.field
			response := doJSON(t, fixture.server.Client(), http.MethodPost, fixture.server.URL+"/v1/work-items", seed.ManagerToken,
				map[string]string{"Idempotency-Key": test.idempotencyKey, "X-Request-ID": requestID}, []byte(test.body))
			assertHTTPInvalidField(t, response, requestID, test.field, test.message)
			if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_items`); got != beforeItems {
				t.Fatalf("work items=%d want=%d", got, beforeItems)
			}
			if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_item_events`); got != beforeEvents {
				t.Fatalf("events=%d want=%d", got, beforeEvents)
			}
			if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM idempotency_keys WHERE idempotency_key=$1`, test.idempotencyKey); got != 0 {
				t.Fatalf("idempotency records=%d", got)
			}
			if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM notification_deliveries`); got != beforeNotifications {
				t.Fatalf("notifications=%d want=%d", got, beforeNotifications)
			}

			validBody := []byte(`{"title":"Quarterly handoff","description":"Inspect loading dock"}`)
			valid := doJSON(t, fixture.server.Client(), http.MethodPost, fixture.server.URL+"/v1/work-items", seed.ManagerToken,
				map[string]string{"Idempotency-Key": test.idempotencyKey, "X-Request-ID": requestID + "-valid"}, validBody)
			assertHTTPResponse(t, valid, http.StatusCreated, `"1"`, "")
			var created struct {
				Data workitems.WorkItem `json:"data"`
			}
			decodeBody(t, valid, &created)
			if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_items WHERE id=$1`, created.Data.ID); got != 1 {
				t.Fatalf("created work items=%d", got)
			}
			if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_item_events WHERE work_item_id=$1 AND event_type='WORK_ITEM_CREATED'`, created.Data.ID); got != 1 {
				t.Fatalf("created events=%d", got)
			}
			if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM idempotency_keys WHERE idempotency_key=$1`, test.idempotencyKey); got != 1 {
				t.Fatalf("idempotency records=%d", got)
			}
			if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM notification_deliveries WHERE work_item_id=$1`, created.Data.ID); got != 0 {
				t.Fatalf("created notifications=%d", got)
			}
		})
	}

	t.Run("assignment reason", func(t *testing.T) {
		fixture := newLiveHTTPFixture(t)
		created, err := fixture.itemService.Create(context.Background(), fixture.manager, workitems.CreateInput{Title: "NUL assignment", IdempotencyKey: "nul-http-assignment-create"})
		if err != nil {
			t.Fatal(err)
		}
		requestID := "request-http-nul-assignment"
		body := []byte(`{"assignee_id":"` + seed.WorkerAliceID + `","reason":"Coverage\u0000 changed"}`)
		response := doJSON(t, fixture.server.Client(), http.MethodPut, fixture.server.URL+"/v1/work-items/"+created.Item.ID+"/assignment", seed.ManagerToken,
			map[string]string{"If-Match": `"1"`, "X-Request-ID": requestID}, body)
		assertHTTPInvalidField(t, response, requestID, "reason", "Reason contains an unsupported character.")
		unchanged, err := fixture.itemService.Get(context.Background(), fixture.manager, created.Item.ID)
		if err != nil || unchanged.Version != 1 || unchanged.Status != workitems.StatusUnassigned || unchanged.AssigneeID != "" {
			t.Fatalf("work item=%#v error=%v", unchanged, err)
		}
		if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_item_events WHERE work_item_id=$1`, created.Item.ID); got != 1 {
			t.Fatalf("events=%d", got)
		}
		if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM notification_deliveries WHERE work_item_id=$1`, created.Item.ID); got != 0 {
			t.Fatalf("notifications=%d", got)
		}

		valid := doJSON(t, fixture.server.Client(), http.MethodPut, fixture.server.URL+"/v1/work-items/"+created.Item.ID+"/assignment", seed.ManagerToken,
			map[string]string{"If-Match": `"1"`, "X-Request-ID": requestID + "-valid"}, []byte(`{"assignee_id":"`+seed.WorkerAliceID+`","reason":"Coverage changed"}`))
		assertHTTPResponse(t, valid, http.StatusOK, `"2"`, "")
		_ = valid.Body.Close()
		if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_item_events WHERE work_item_id=$1 AND event_type='WORK_ITEM_ASSIGNED'`, created.Item.ID); got != 1 {
			t.Fatalf("assignment events=%d", got)
		}
		if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM notification_deliveries WHERE work_item_id=$1`, created.Item.ID); got != 1 {
			t.Fatalf("notifications=%d", got)
		}
	})

	t.Run("replay reason", func(t *testing.T) {
		fixture := newLiveHTTPFixture(t)
		created, err := fixture.itemService.Create(context.Background(), fixture.manager, workitems.CreateInput{Title: "NUL replay", IdempotencyKey: "nul-http-replay-create"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = fixture.itemService.Assign(context.Background(), fixture.manager, created.Item.ID, workitems.AssignmentInput{AssigneeID: seed.WorkerAliceID, ExpectedVersion: 1}); err != nil {
			t.Fatal(err)
		}
		delivery, found, err := fixture.store.ClaimNext(context.Background(), notifications.ClaimRequest{WorkerID: "worker-http-nul", LeaseDuration: time.Minute, MaxAttempts: 3})
		if err != nil || !found {
			t.Fatalf("claim found=%v error=%v", found, err)
		}
		if err = fixture.store.Complete(context.Background(), notifications.Completion{DeliveryID: delivery.ID, WorkerID: "worker-http-nul", LeaseToken: delivery.LeaseToken, AttemptNumber: delivery.AttemptCount, Outcome: notifications.OutcomeFailed, FailureCode: notifications.FailureSMTPRecipientRejected}); err != nil {
			t.Fatal(err)
		}
		requestID := "request-http-nul-replay"
		response := doJSON(t, fixture.server.Client(), http.MethodPost, fixture.server.URL+"/v1/notifications/"+delivery.ID+"/replay", seed.ManagerToken,
			map[string]string{"X-Request-ID": requestID}, []byte(`{"reason":"Provider\u0000 recovered"}`))
		assertHTTPInvalidField(t, response, requestID, "reason", "Reason contains an unsupported character.")
		unchanged, err := fixture.store.Get(context.Background(), delivery.ID)
		if err != nil || unchanged.Status != notifications.StatusFailed || unchanged.DeliveryCycle != 1 || len(unchanged.Replays) != 0 || len(unchanged.Attempts) != 1 {
			t.Fatalf("delivery=%#v error=%v", unchanged, err)
		}

		validReason := "Operator verified recipient"
		valid := doJSON(t, fixture.server.Client(), http.MethodPost, fixture.server.URL+"/v1/notifications/"+delivery.ID+"/replay", seed.ManagerToken,
			map[string]string{"X-Request-ID": requestID + "-valid"}, []byte(`{"reason":"`+validReason+`"}`))
		assertHTTPResponse(t, valid, http.StatusAccepted, "", "")
		_ = valid.Body.Close()
		replayed, err := fixture.store.Get(context.Background(), delivery.ID)
		if err != nil || replayed.Status != notifications.StatusPending || replayed.DeliveryCycle != 2 || len(replayed.Replays) != 1 || replayed.Replays[0].Reason != validReason {
			t.Fatalf("delivery=%#v error=%v", replayed, err)
		}
	})
}

func TestHTTPRejectsRawInvalidUTF8IdempotencyKeyWithoutInternalFailure(t *testing.T) {
	var logs bytes.Buffer
	registry := metricspkg.New(nil, nil)
	fixture := newLiveHTTPFixtureWithObservability(t, slog.New(slog.NewJSONHandler(&logs, nil)), registry)
	beforeItems := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_items`)
	beforeEvents := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_item_events`)
	beforeKeys := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM idempotency_keys`)
	beforeNotifications := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM notification_deliveries`)
	requestID := "request-invalid-utf8-idempotency"

	response := doRawCreate(t, fixture.server.URL, seed.ManagerToken, requestID, append([]byte("  invalid-"), []byte{0xff, '-', 'k', 'e', 'y', ' ', ' '}...))
	assertHTTPInvalidField(t, response, requestID, "Idempotency-Key", "The Idempotency-Key header must contain valid UTF-8.")
	if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_items`); got != beforeItems {
		t.Fatalf("work items=%d want=%d", got, beforeItems)
	}
	if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_item_events`); got != beforeEvents {
		t.Fatalf("events=%d want=%d", got, beforeEvents)
	}
	if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM idempotency_keys`); got != beforeKeys {
		t.Fatalf("idempotency keys=%d want=%d", got, beforeKeys)
	}
	if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM notification_deliveries`); got != beforeNotifications {
		t.Fatalf("notifications=%d want=%d", got, beforeNotifications)
	}

	records := decodeJSONLogRecords(t, logs.Bytes())
	assertLogRecord(t, records, "api mutation", http.StatusBadRequest, "INVALID_INPUT")
	assertLogRecord(t, records, "http request", http.StatusBadRequest, "")
	for _, record := range records {
		if record["msg"] == "request failed" || record["status"] == float64(http.StatusInternalServerError) || record["error_code"] == "INTERNAL_ERROR" {
			t.Fatalf("unexpected internal-failure log=%v all=%v", record, records)
		}
	}
	logOutput := logs.String()
	for _, forbidden := range []string{"SQLSTATE", "invalid byte sequence"} {
		if strings.Contains(logOutput, forbidden) {
			t.Errorf("logs contain %q:\n%s", forbidden, logOutput)
		}
	}
	if bytes.Contains(logs.Bytes(), []byte{0xff}) {
		t.Fatalf("logs contain the malformed header byte: %q", logs.Bytes())
	}
	metricsResponse := httptest.NewRecorder()
	registry.ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricOutput := metricsResponse.Body.String()
	if !hasExactMetricLine(metricOutput, `work_management_http_requests_total{method="POST",route="/v1/work-items",status="400"} 1`) {
		t.Fatalf("400 metric missing:\n%s", metricOutput)
	}
	for _, line := range strings.Split(metricOutput, "\n") {
		if strings.HasPrefix(line, "work_management_http_") && strings.Contains(line, `route="/v1/work-items"`) && strings.Contains(line, `status="500"`) {
			t.Fatalf("false 500 metric present: %s\n%s", line, metricOutput)
		}
	}

	unauthenticated := doRawCreate(t, fixture.server.URL, "", requestID+"-unauthenticated", append([]byte("invalid-"), 0xff))
	assertHTTPError(t, unauthenticated, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED")
	denied := doRawCreate(t, fixture.server.URL, seed.WorkerAliceToken, requestID+"-worker", append([]byte("invalid-"), 0xff))
	assertHTTPError(t, denied, http.StatusForbidden, "PERMISSION_DENIED")
	if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_items`); got != beforeItems {
		t.Fatalf("work items after denied request=%d want=%d", got, beforeItems)
	}

	valid := doJSON(t, fixture.server.Client(), http.MethodPost, fixture.server.URL+"/v1/work-items", seed.ManagerToken,
		map[string]string{"Idempotency-Key": "válido-界", "X-Request-ID": requestID + "-valid"}, []byte(`{"title":"Valid after invalid UTF-8"}`))
	assertHTTPResponse(t, valid, http.StatusCreated, `"1"`, "")
	_ = valid.Body.Close()
}

func decodeJSONLogRecords(t *testing.T, payload []byte) []map[string]any {
	t.Helper()
	var records []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func assertLogRecord(t *testing.T, records []map[string]any, message string, status int, errorCode string) {
	t.Helper()
	for _, record := range records {
		if record["msg"] != message || record["status"] != float64(status) {
			continue
		}
		if errorCode == "" || record["error_code"] == errorCode {
			return
		}
	}
	t.Fatalf("missing log msg=%q status=%d error_code=%q in %v", message, status, errorCode, records)
}

func hasExactMetricLine(payload, want string) bool {
	for _, line := range strings.Split(payload, "\n") {
		if line == want {
			return true
		}
	}
	return false
}

func TestNULValidationPreservesAuthorizationPrecedence(t *testing.T) {
	fixture := newLiveHTTPFixture(t)
	beforeItems := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_items`)
	response := doJSON(t, fixture.server.Client(), http.MethodPost, fixture.server.URL+"/v1/work-items", seed.WorkerAliceToken,
		map[string]string{"Idempotency-Key": "nul-worker-create", "X-Request-ID": "request-nul-worker-create"}, []byte(`{"title":"Denied\u0000 title"}`))
	assertHTTPError(t, response, http.StatusForbidden, "PERMISSION_DENIED")
	if got := postgrestest.Count(t, fixture.db, `SELECT count(*) FROM work_items`); got != beforeItems {
		t.Fatalf("work items=%d want=%d", got, beforeItems)
	}
}

type liveHTTPFixture struct {
	db          *sql.DB
	server      *httptest.Server
	itemService *workitems.Service
	store       *notifications.Store
	manager     auth.Principal
}

func newLiveHTTPFixture(t *testing.T) liveHTTPFixture {
	t.Helper()
	return newLiveHTTPFixtureWithObservability(t, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

func newLiveHTTPFixtureWithObservability(t *testing.T, logger Logger, metricRecorder MetricRecorder) liveHTTPFixture {
	t.Helper()
	db := postgrestest.Open(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	if _, err := seed.Apply(context.Background(), db, clock.Fixed{Time: now}); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(auth.PostgresTokenStore{DB: db}, clock.Fixed{Time: now})
	itemService, err := workitems.NewService(db, clock.Fixed{Time: now}, id.NewUUID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := notifications.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	notificationService, err := notifications.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewServer(ServerConfig{Logger: logger, Authenticator: authService, WorkItems: itemService, Notifications: notificationService, RequestID: id.NewUUID, MaxBodyBytes: 1 << 20, MetricRecorder: metricRecorder})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return liveHTTPFixture{
		db:          db,
		server:      server,
		itemService: itemService,
		store:       store,
		manager:     auth.Principal{ID: seed.ManagerID, Role: auth.RoleManager, DisplayName: "Maya Manager"},
	}
}

func doRawCreate(t *testing.T, serverURL, token, requestID string, idempotencyKey []byte) *http.Response {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", parsed.Host, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if err = connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"title":"Invalid UTF-8 idempotency key"}`)
	payload := []byte(fmt.Sprintf("POST /v1/work-items HTTP/1.1\r\nHost: %s\r\n", parsed.Host))
	if token != "" {
		payload = append(payload, []byte("Authorization: Bearer "+token+"\r\n")...)
	}
	payload = append(payload, []byte(fmt.Sprintf("Content-Type: application/json\r\nContent-Length: %d\r\nX-Request-ID: %s\r\nIdempotency-Key: ", len(body), requestID))...)
	payload = append(payload, idempotencyKey...)
	payload = append(payload, []byte("\r\nConnection: close\r\n\r\n")...)
	payload = append(payload, body...)
	if _, err = connection.Write(payload); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(responseBody))
	return response
}

func assertHTTPInvalidField(t *testing.T, response *http.Response, requestID, field, message string) {
	t.Helper()
	assertHTTPResponse(t, response, http.StatusBadRequest, "", "")
	if got := response.Header.Get("X-Request-ID"); got != requestID {
		t.Fatalf("X-Request-ID=%q want=%q", got, requestID)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q", got)
	}
	var body errorEnvelope
	decodeBody(t, response, &body)
	if body.Error.Code != "INVALID_INPUT" || body.Error.Message != message || body.Error.RequestID != requestID || len(body.Error.Details) != 1 || body.Error.Details["field"] != field {
		t.Fatalf("error=%#v", body.Error)
	}
	if strings.Contains(strings.ToLower(body.Error.Message), "postgres") {
		t.Fatalf("message exposes persistence internals: %q", body.Error.Message)
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
