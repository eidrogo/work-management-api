package metrics

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

type BacklogReader interface {
	Backlog(context.Context) (notifications.Backlog, error)
}

const (
	maxHTTPSeries   = 1024
	backlogCacheTTL = 5 * time.Second
	backlogTimeout  = time.Second
)

type httpKey struct {
	method string
	route  string
	status int
}
type mutationKey struct{ operation, outcome string }
type httpSample struct {
	count    uint64
	duration time.Duration
}
type backlogCache struct {
	value             notifications.Backlog
	hasValue          bool
	refreshAfter      time.Time
	refreshing        bool
	lastRefreshFailed bool
}
type Registry struct {
	mu                    sync.Mutex
	http                  map[httpKey]httpSample
	httpSeriesDropped     uint64
	authFailures          uint64
	authorizationFailures uint64
	mutations             map[mutationKey]uint64
	notificationAttempts  map[string]uint64

	backlogMu    sync.Mutex
	backlogState backlogCache
	backlog      BacklogReader
	now          func() time.Time
}

func New(backlog BacklogReader, now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{http: map[httpKey]httpSample{}, mutations: map[mutationKey]uint64{}, notificationAttempts: map[string]uint64{}, backlog: backlog, now: now}
}
func (r *Registry) RecordHTTP(method, route string, status int, duration time.Duration) {
	if r == nil {
		return
	}
	key := httpKey{method: canonicalHTTPMethod(method), route: canonicalHTTPRoute(route), status: status}
	r.mu.Lock()
	defer r.mu.Unlock()
	sample, exists := r.http[key]
	if !exists && len(r.http) >= maxHTTPSeries {
		r.httpSeriesDropped++
		return
	}
	sample.count++
	sample.duration += duration
	r.http[key] = sample
}

func canonicalHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut:
		return method
	default:
		return "OTHER"
	}
}

func canonicalHTTPRoute(route string) string {
	switch route {
	case "/v1/work-items",
		"/v1/me/work-items",
		"/v1/work-items/{id}",
		"/v1/work-items/{id}/assignment",
		"/v1/work-items/{id}/start",
		"/v1/work-items/{id}/complete",
		"/v1/work-items/{id}/history",
		"/v1/notifications",
		"/v1/notifications/{id}",
		"/v1/notifications/{id}/replay",
		"/unknown":
		return route
	default:
		return "/unknown"
	}
}

func (r *Registry) cachedBacklog(ctx context.Context) (notifications.Backlog, bool) {
	if r.backlog == nil {
		return notifications.Backlog{}, false
	}

	now := r.now()
	r.backlogMu.Lock()
	if r.backlogState.refreshing || now.Before(r.backlogState.refreshAfter) {
		value := cloneBacklog(r.backlogState.value)
		failed := r.backlogState.lastRefreshFailed || !r.backlogState.hasValue
		r.backlogMu.Unlock()
		return value, failed
	}
	r.backlogState.refreshing = true
	r.backlogMu.Unlock()

	queryCtx, cancel := context.WithTimeout(ctx, backlogTimeout)
	value, err := r.backlog.Backlog(queryCtx)
	cancel()
	completedAt := r.now()

	r.backlogMu.Lock()
	if err == nil {
		r.backlogState.value = cloneBacklog(value)
		r.backlogState.hasValue = true
		r.backlogState.lastRefreshFailed = false
	} else {
		r.backlogState.lastRefreshFailed = true
	}
	r.backlogState.refreshAfter = completedAt.Add(backlogCacheTTL)
	r.backlogState.refreshing = false
	snapshot := cloneBacklog(r.backlogState.value)
	failed := r.backlogState.lastRefreshFailed || !r.backlogState.hasValue
	r.backlogMu.Unlock()
	return snapshot, failed
}

func cloneBacklog(value notifications.Backlog) notifications.Backlog {
	result := value
	if value.OldestPendingAt != nil {
		oldest := *value.OldestPendingAt
		result.OldestPendingAt = &oldest
	}
	return result
}

func (r *Registry) RecordAuthFailure() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.authFailures++
	r.mu.Unlock()
}
func (r *Registry) RecordAuthorizationFailure() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.authorizationFailures++
	r.mu.Unlock()
}
func (r *Registry) RecordMutation(operation, outcome string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.mutations[mutationKey{operation, outcome}]++
	r.mu.Unlock()
}
func (r *Registry) RecordNotificationAttempt(outcome string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.notificationAttempts[outcome]++
	r.mu.Unlock()
}
func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	r.mu.Lock()
	httpValues := map[httpKey]httpSample{}
	for key, value := range r.http {
		httpValues[key] = value
	}
	httpSeriesDropped := r.httpSeriesDropped
	authFailures, authorizationFailures := r.authFailures, r.authorizationFailures
	mutations := map[mutationKey]uint64{}
	for key, value := range r.mutations {
		mutations[key] = value
	}
	attempts := map[string]uint64{}
	for key, value := range r.notificationAttempts {
		attempts[key] = value
	}
	r.mu.Unlock()
	var builder strings.Builder
	keys := make([]httpKey, 0, len(httpValues))
	for key := range httpValues {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})
	for _, key := range keys {
		value := httpValues[key]
		fmt.Fprintf(&builder, "work_management_http_requests_total{method=%s,route=%s,status=%s} %d\n", strconv.Quote(key.method), strconv.Quote(key.route), strconv.Quote(strconv.Itoa(key.status)), value.count)
	}
	for _, key := range keys {
		value := httpValues[key]
		fmt.Fprintf(&builder, "work_management_http_request_duration_seconds_sum{method=%s,route=%s,status=%s} %.6f\n", strconv.Quote(key.method), strconv.Quote(key.route), strconv.Quote(strconv.Itoa(key.status)), value.duration.Seconds())
	}
	fmt.Fprintf(&builder, "work_management_http_series_dropped_total %d\n", httpSeriesDropped)
	fmt.Fprintf(&builder, "work_management_authentication_failures_total %d\n", authFailures)
	fmt.Fprintf(&builder, "work_management_authorization_failures_total %d\n", authorizationFailures)
	mutationKeys := make([]mutationKey, 0, len(mutations))
	for key := range mutations {
		mutationKeys = append(mutationKeys, key)
	}
	sort.Slice(mutationKeys, func(i, j int) bool {
		if mutationKeys[i].operation != mutationKeys[j].operation {
			return mutationKeys[i].operation < mutationKeys[j].operation
		}
		return mutationKeys[i].outcome < mutationKeys[j].outcome
	})
	for _, key := range mutationKeys {
		value := mutations[key]
		fmt.Fprintf(&builder, "work_management_mutations_total{operation=%s,outcome=%s} %d\n", strconv.Quote(key.operation), strconv.Quote(key.outcome), value)
	}
	attemptOutcomes := make([]string, 0, len(attempts))
	for outcome := range attempts {
		attemptOutcomes = append(attemptOutcomes, outcome)
	}
	sort.Strings(attemptOutcomes)
	for _, outcome := range attemptOutcomes {
		value := attempts[outcome]
		fmt.Fprintf(&builder, "work_management_notification_attempts_total{outcome=%s} %d\n", strconv.Quote(outcome), value)
	}
	backlog, failed := r.cachedBacklog(req.Context())
	scrapeError := 0
	if failed {
		scrapeError = 1
	}
	fmt.Fprintf(&builder, "work_management_notification_pending %d\n", backlog.Pending)
	fmt.Fprintf(&builder, "work_management_notification_processing %d\n", backlog.Processing)
	fmt.Fprintf(&builder, "work_management_notification_failed %d\n", backlog.Failed)
	age := float64(0)
	if backlog.OldestPendingAt != nil {
		age = r.now().UTC().Sub(backlog.OldestPendingAt.UTC()).Seconds()
		if age < 0 {
			age = 0
		}
	}
	fmt.Fprintf(&builder, "work_management_notification_oldest_pending_age_seconds %.0f\n", age)
	fmt.Fprintf(&builder, "work_management_metrics_scrape_error %d\n", scrapeError)
	_, _ = w.Write([]byte(builder.String()))
}
