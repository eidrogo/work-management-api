package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

func TestRegistryExportsCoreMetrics(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	oldest := now.Add(-90 * time.Second)
	r := New(&fakeBacklog{value: notifications.Backlog{Pending: 2, Failed: 1, OldestPendingAt: &oldest}}, func() time.Time { return now })
	r.RecordHTTP("GET", "/v1/work-items/{id}", 200, time.Millisecond)
	r.RecordAuthFailure()
	r.RecordAuthorizationFailure()
	r.RecordMutation("assignment", "success")
	r.RecordNotificationAttempt("SENT")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{"work_management_authentication_failures_total 1", "work_management_authorization_failures_total 1", "work_management_notification_pending 2", "work_management_notification_oldest_pending_age_seconds 90"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q", expected)
		}
	}
}

func TestRegistryCanonicalizesHTTPMethodAndRouteLabels(t *testing.T) {
	r := New(nil, nil)
	r.RecordHTTP("GET", "/v1/work-items", 200, time.Millisecond)
	r.RecordHTTP("POST", "/v1/work-items/{id}/start", 201, time.Millisecond)
	r.RecordHTTP("PUT", "/v1/work-items/{id}/assignment", 202, time.Millisecond)
	r.RecordHTTP("get", "/v1/work-items/attacker", 400, time.Millisecond)
	r.RecordHTTP("DELETE", "/attacker-controlled", 401, time.Millisecond)
	body := renderRegistry(t, r)
	for _, expected := range []string{
		`method="GET",route="/v1/work-items",status="200"`,
		`method="POST",route="/v1/work-items/{id}/start",status="201"`,
		`method="PUT",route="/v1/work-items/{id}/assignment",status="202"`,
		`method="OTHER",route="/unknown",status="400"`,
		`method="OTHER",route="/unknown",status="401"`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("missing %q in:\n%s", expected, body)
		}
	}
	for _, rejected := range []string{`method="get"`, `method="DELETE"`, "attacker-controlled", "/v1/work-items/attacker"} {
		if strings.Contains(body, rejected) {
			t.Errorf("unexpected %q in:\n%s", rejected, body)
		}
	}
}

func TestRegistryBoundsThousandsOfUniqueMethodsAndRoutes(t *testing.T) {
	r := New(nil, nil)
	for index := 0; index < 10_000; index++ {
		r.RecordHTTP(fmt.Sprintf("METHOD-%d", index), fmt.Sprintf("/v1/work-items/id/action-%d", index), 200, time.Microsecond)
	}
	if len(r.http) != 1 {
		t.Fatalf("retained HTTP keys=%d", len(r.http))
	}
}

func TestRegistryCapsHTTPSeriesAndCountsDrops(t *testing.T) {
	r := New(nil, nil)
	for status := 1; status <= 1_025; status++ {
		r.RecordHTTP("GET", "/v1/work-items", status, time.Microsecond)
	}
	if len(r.http) != 1_024 {
		t.Fatalf("retained HTTP keys=%d", len(r.http))
	}
	if !hasExactMetricLine(renderRegistry(t, r), "work_management_http_series_dropped_total 1") {
		t.Fatalf("drop counter missing")
	}
}

func TestRegistryConcurrentRecordersCannotExceedHTTPSeriesCap(t *testing.T) {
	r := New(nil, nil)
	const observations = 2_048
	var wait sync.WaitGroup
	wait.Add(observations)
	for status := 1; status <= observations; status++ {
		status := status
		go func() {
			defer wait.Done()
			r.RecordHTTP("GET", "/v1/work-items", status, time.Microsecond)
		}()
	}
	wait.Wait()
	if len(r.http) != maxHTTPSeries {
		t.Fatalf("retained HTTP keys=%d", len(r.http))
	}
	if r.httpSeriesDropped != observations-maxHTTPSeries {
		t.Fatalf("dropped=%d", r.httpSeriesDropped)
	}
}

func TestRegistryUpdatesExistingHTTPSeriesAfterCap(t *testing.T) {
	r := New(nil, nil)
	r.RecordHTTP("GET", "/v1/work-items", 200, time.Second)
	for status := 1_000; status < 2_023; status++ {
		r.RecordHTTP("GET", "/v1/work-items", status, time.Microsecond)
	}
	r.RecordHTTP("GET", "/v1/work-items", 9_999, time.Hour)
	r.RecordHTTP("GET", "/v1/work-items", 200, 2*time.Second)
	body := renderRegistry(t, r)
	if len(r.http) != 1_024 {
		t.Fatalf("retained HTTP keys=%d", len(r.http))
	}
	if !strings.Contains(body, `work_management_http_requests_total{method="GET",route="/v1/work-items",status="200"} 2`) {
		t.Fatalf("existing count not updated:\n%s", body)
	}
	if !strings.Contains(body, `work_management_http_request_duration_seconds_sum{method="GET",route="/v1/work-items",status="200"} 3.000000`) {
		t.Fatalf("existing duration not updated:\n%s", body)
	}
	if !hasExactMetricLine(body, "work_management_http_series_dropped_total 1") {
		t.Fatalf("drop counter missing")
	}
}

func TestRegistryExportsUniqueStatusLabelledHTTPSeries(t *testing.T) {
	r := New(nil, nil)
	r.RecordHTTP("GET", "/v1/work-items", 200, time.Second)
	r.RecordHTTP("GET", "/v1/work-items", 404, 2*time.Second)
	body := renderRegistry(t, r)
	leftHandSides := map[string]struct{}{}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "work_management_http_requests_total") || strings.HasPrefix(line, "work_management_http_request_duration_seconds_sum") {
			leftHandSides[strings.Fields(line)[0]] = struct{}{}
			if !strings.Contains(line, `status="`) {
				t.Errorf("HTTP series omits status: %s", line)
			}
		}
	}
	if len(leftHandSides) != 4 {
		t.Fatalf("unique HTTP left-hand series=%d body:\n%s", len(leftHandSides), body)
	}
}

func TestRegistryExpositionIsDeterministic(t *testing.T) {
	populate := func(reverse bool) *Registry {
		r := New(nil, nil)
		httpSamples := []struct {
			method, route string
			status        int
		}{{"PUT", "/v1/work-items/{id}/assignment", 409}, {"GET", "/v1/work-items", 200}, {"POST", "/v1/work-items/{id}/start", 200}}
		mutationSamples := [][2]string{{"start", "success"}, {"assignment", "conflict"}, {"assignment", "success"}}
		attemptSamples := []string{"SENT", "FAILED", "RETRY_SCHEDULED"}
		if reverse {
			slices.Reverse(httpSamples)
			slices.Reverse(mutationSamples)
			slices.Reverse(attemptSamples)
		}
		for _, sample := range httpSamples {
			r.RecordHTTP(sample.method, sample.route, sample.status, time.Millisecond)
		}
		for _, sample := range mutationSamples {
			r.RecordMutation(sample[0], sample[1])
		}
		for _, outcome := range attemptSamples {
			r.RecordNotificationAttempt(outcome)
		}
		return r
	}
	forward := renderRegistry(t, populate(false))
	reverse := renderRegistry(t, populate(true))
	if forward != reverse {
		t.Fatalf("forward:\n%s\nreverse:\n%s", forward, reverse)
	}
	if !hasExactMetricLine(forward, "work_management_http_series_dropped_total 0") {
		t.Fatalf("deterministic exposition omits scalar drop counter")
	}
	assertMetricOrder(t, forward,
		`work_management_mutations_total{operation="assignment",outcome="conflict"}`,
		`work_management_mutations_total{operation="assignment",outcome="success"}`,
		`work_management_mutations_total{operation="start",outcome="success"}`,
	)
	assertMetricOrder(t, forward,
		`work_management_notification_attempts_total{outcome="FAILED"}`,
		`work_management_notification_attempts_total{outcome="RETRY_SCHEDULED"}`,
		`work_management_notification_attempts_total{outcome="SENT"}`,
	)
}

func TestHTTPSeriesDropCounterHasNoLabels(t *testing.T) {
	r := New(nil, nil)
	for status := 1; status <= 1_025; status++ {
		r.RecordHTTP("GET", "/v1/work-items", status, time.Microsecond)
	}
	body := renderRegistry(t, r)
	if !hasExactMetricLine(body, "work_management_http_series_dropped_total 1") {
		t.Fatalf("missing exact scalar drop line")
	}
	if strings.Contains(body, "work_management_http_series_dropped_total{") || strings.Contains(body, "work_management_http_series_dropped_total{}") {
		t.Fatalf("drop counter has labels:\n%s", body)
	}
}

func TestRegistryCachesBacklogForFiveSeconds(t *testing.T) {
	clock := newManualClock(time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC))
	reader := &scriptedBacklogReader{steps: []backlogStep{
		{value: notifications.Backlog{Pending: 1}},
		{value: notifications.Backlog{Pending: 2}},
	}}
	r := New(reader, clock.Now)
	if body := renderRegistry(t, r); !hasExactMetricLine(body, "work_management_notification_pending 1") {
		t.Fatalf("initial body:\n%s", body)
	}
	clock.Advance(5*time.Second - time.Millisecond)
	if body := renderRegistry(t, r); !hasExactMetricLine(body, "work_management_notification_pending 1") {
		t.Fatalf("cached body:\n%s", body)
	}
	if calls := reader.Calls(); calls != 1 {
		t.Fatalf("calls before expiry=%d", calls)
	}
	clock.Advance(time.Millisecond)
	if body := renderRegistry(t, r); !hasExactMetricLine(body, "work_management_notification_pending 2") {
		t.Fatalf("refreshed body:\n%s", body)
	}
	if calls := reader.Calls(); calls != 2 {
		t.Fatalf("calls after expiry=%d", calls)
	}
}

func TestRegistryBacklogRefreshSingleFlightServesLastGood(t *testing.T) {
	clock := newManualClock(time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC))
	refreshEntered := make(chan struct{})
	releaseRefresh := make(chan struct{})
	reader := &scriptedBacklogReader{steps: []backlogStep{
		{value: notifications.Backlog{Pending: 3}},
		{value: notifications.Backlog{Pending: 7}, entered: refreshEntered, release: releaseRefresh},
	}}
	r := New(reader, clock.Now)
	_ = renderRegistry(t, r)
	clock.Advance(5 * time.Second)
	leaderResult := make(chan string, 1)
	go func() {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
		leaderResult <- response.Body.String()
	}()
	select {
	case <-refreshEntered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}

	const concurrentScrapes = 32
	results := make(chan string, concurrentScrapes)
	for index := 0; index < concurrentScrapes; index++ {
		go func() {
			response := httptest.NewRecorder()
			r.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
			results <- response.Body.String()
		}()
	}
	for index := 0; index < concurrentScrapes; index++ {
		select {
		case body := <-results:
			if !hasExactMetricLine(body, "work_management_notification_pending 3") || !hasExactMetricLine(body, "work_management_metrics_scrape_error 0") {
				t.Fatalf("stale scrape %d:\n%s", index, body)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent scrape waited for refresh")
		}
	}
	if calls := reader.Calls(); calls != 2 {
		t.Fatalf("reader calls during refresh=%d", calls)
	}
	close(releaseRefresh)
	select {
	case body := <-leaderResult:
		if !hasExactMetricLine(body, "work_management_notification_pending 7") {
			t.Fatalf("leader body:\n%s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not complete")
	}
}

func TestRegistryBacklogFailureRetainsLastGood(t *testing.T) {
	clock, reader, r := backlogFailureRegistry()
	seedBody := renderRegistry(t, r)
	if !hasExactMetricLine(seedBody, "work_management_metrics_scrape_error 0") {
		t.Fatalf("seed body:\n%s", seedBody)
	}
	clock.Advance(5 * time.Second)
	body := renderRegistry(t, r)
	for _, line := range []string{
		"work_management_notification_pending 4",
		"work_management_notification_processing 2",
		"work_management_notification_failed 1",
		"work_management_notification_oldest_pending_age_seconds 40",
		"work_management_metrics_scrape_error 1",
	} {
		if !hasExactMetricLine(body, line) {
			t.Errorf("missing %q in:\n%s", line, body)
		}
	}
	if calls := reader.Calls(); calls != 2 {
		t.Fatalf("reader calls=%d", calls)
	}
}

func TestRegistryBacklogFailureIsThrottledForFiveSeconds(t *testing.T) {
	clock, reader, r := backlogFailureRegistry()
	_ = renderRegistry(t, r)
	clock.Advance(5 * time.Second)
	_ = renderRegistry(t, r)
	for index := 0; index < 10; index++ {
		_ = renderRegistry(t, r)
	}
	if calls := reader.Calls(); calls != 2 {
		t.Fatalf("calls during failure TTL=%d", calls)
	}
	clock.Advance(5 * time.Second)
	body := renderRegistry(t, r)
	if calls := reader.Calls(); calls != 3 {
		t.Fatalf("calls after failure TTL=%d", calls)
	}
	if !hasExactMetricLine(body, "work_management_notification_pending 9") || !hasExactMetricLine(body, "work_management_metrics_scrape_error 0") {
		t.Fatalf("recovered body:\n%s", body)
	}
}

func TestRegistryBacklogSuccessClearsScrapeError(t *testing.T) {
	clock, _, r := backlogFailureRegistry()
	_ = renderRegistry(t, r)
	clock.Advance(5 * time.Second)
	if body := renderRegistry(t, r); !hasExactMetricLine(body, "work_management_metrics_scrape_error 1") {
		t.Fatalf("failure body:\n%s", body)
	}
	clock.Advance(5 * time.Second)
	if body := renderRegistry(t, r); !hasExactMetricLine(body, "work_management_metrics_scrape_error 0") {
		t.Fatalf("success body:\n%s", body)
	}
}

func TestRegistryBacklogInitialFailureHasNoFalseGoodValue(t *testing.T) {
	reader := &scriptedBacklogReader{steps: []backlogStep{{err: errors.New("database unavailable")}}}
	r := New(reader, nil)
	body := renderRegistry(t, r)
	for _, line := range []string{
		"work_management_notification_pending 0",
		"work_management_notification_processing 0",
		"work_management_notification_failed 0",
		"work_management_notification_oldest_pending_age_seconds 0",
		"work_management_metrics_scrape_error 1",
	} {
		if !hasExactMetricLine(body, line) {
			t.Errorf("missing %q in:\n%s", line, body)
		}
	}
}

func TestRegistryNilBacklogHasNoScrapeError(t *testing.T) {
	body := renderRegistry(t, New(nil, nil))
	if !hasExactMetricLine(body, "work_management_metrics_scrape_error 0") {
		t.Fatalf("body:\n%s", body)
	}
}

func TestRegistryBacklogCacheClonesOldestTimestamp(t *testing.T) {
	clock := newManualClock(time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC))
	oldest := clock.Now().Add(-20 * time.Second)
	reader := &scriptedBacklogReader{steps: []backlogStep{{value: notifications.Backlog{Pending: 1, OldestPendingAt: &oldest}}}}
	r := New(reader, clock.Now)
	body := renderRegistry(t, r)
	if !hasExactMetricLine(body, "work_management_notification_oldest_pending_age_seconds 20") {
		t.Fatalf("initial body:\n%s", body)
	}
	oldest = oldest.Add(-time.Hour)
	clock.Advance(time.Second)
	body = renderRegistry(t, r)
	if !hasExactMetricLine(body, "work_management_notification_oldest_pending_age_seconds 21") {
		t.Fatalf("cached timestamp was aliased:\n%s", body)
	}
}

type backlogStep struct {
	value   notifications.Backlog
	err     error
	entered chan struct{}
	release <-chan struct{}
}

type scriptedBacklogReader struct {
	mu       sync.Mutex
	steps    []backlogStep
	calls    int
	signaled map[int]bool
}

func (reader *scriptedBacklogReader) Backlog(ctx context.Context) (notifications.Backlog, error) {
	reader.mu.Lock()
	index := reader.calls
	reader.calls++
	if index >= len(reader.steps) {
		index = len(reader.steps) - 1
	}
	step := reader.steps[index]
	if reader.signaled == nil {
		reader.signaled = make(map[int]bool)
	}
	shouldSignal := step.entered != nil && !reader.signaled[index]
	reader.signaled[index] = true
	reader.mu.Unlock()
	if shouldSignal {
		close(step.entered)
	}
	if step.release != nil {
		select {
		case <-step.release:
		case <-ctx.Done():
			return notifications.Backlog{}, ctx.Err()
		}
	}
	return step.value, step.err
}

func (reader *scriptedBacklogReader) Calls() int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.calls
}

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newManualClock(now time.Time) *manualClock { return &manualClock{now: now} }

func (clock *manualClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *manualClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

func backlogFailureRegistry() (*manualClock, *scriptedBacklogReader, *Registry) {
	clock := newManualClock(time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC))
	oldest := clock.Now().Add(-35 * time.Second)
	reader := &scriptedBacklogReader{steps: []backlogStep{
		{value: notifications.Backlog{Pending: 4, Processing: 2, Failed: 1, OldestPendingAt: &oldest}},
		{err: errors.New("database unavailable")},
		{value: notifications.Backlog{Pending: 9}},
	}}
	return clock, reader, New(reader, clock.Now)
}

func renderRegistry(t *testing.T, r *Registry) string {
	t.Helper()
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 {
		t.Fatalf("status=%d", response.Code)
	}
	return response.Body.String()
}

func hasExactMetricLine(body, want string) bool {
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if line == want {
			return true
		}
	}
	return false
}

func assertMetricOrder(t *testing.T, body string, values ...string) {
	t.Helper()
	last := -1
	for _, value := range values {
		index := strings.Index(body, value)
		if index <= last {
			t.Fatalf("%q order in:\n%s", values, body)
		}
		last = index
	}
}

type fakeBacklog struct{ value notifications.Backlog }

func (reader *fakeBacklog) Backlog(context.Context) (notifications.Backlog, error) {
	return reader.value, nil
}
