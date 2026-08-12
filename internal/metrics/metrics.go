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
type httpKey struct{ method, route, status string }
type mutationKey struct{ operation, outcome string }
type httpSample struct {
	count    uint64
	duration time.Duration
}
type Registry struct {
	mu                                  sync.Mutex
	http                                map[httpKey]httpSample
	authFailures, authorizationFailures uint64
	mutations                           map[mutationKey]uint64
	notificationAttempts                map[string]uint64
	backlog                             BacklogReader
	now                                 func() time.Time
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
	r.mu.Lock()
	defer r.mu.Unlock()
	key := httpKey{method, route, strconv.Itoa(status)}
	sample := r.http[key]
	sample.count++
	sample.duration += duration
	r.http[key] = sample
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
		fmt.Fprintf(&builder, "work_management_http_requests_total{method=%s,route=%s,status=%s} %d\n", strconv.Quote(key.method), strconv.Quote(key.route), strconv.Quote(key.status), value.count)
		fmt.Fprintf(&builder, "work_management_http_request_duration_seconds_sum{method=%s,route=%s} %.6f\n", strconv.Quote(key.method), strconv.Quote(key.route), value.duration.Seconds())
	}
	fmt.Fprintf(&builder, "work_management_authentication_failures_total %d\n", authFailures)
	fmt.Fprintf(&builder, "work_management_authorization_failures_total %d\n", authorizationFailures)
	for key, value := range mutations {
		fmt.Fprintf(&builder, "work_management_mutations_total{operation=%s,outcome=%s} %d\n", strconv.Quote(key.operation), strconv.Quote(key.outcome), value)
	}
	for outcome, value := range attempts {
		fmt.Fprintf(&builder, "work_management_notification_attempts_total{outcome=%s} %d\n", strconv.Quote(outcome), value)
	}
	backlog := notifications.Backlog{}
	scrapeError := 0
	if r.backlog != nil {
		ctx, cancel := context.WithTimeout(req.Context(), time.Second)
		value, err := r.backlog.Backlog(ctx)
		cancel()
		if err != nil {
			scrapeError = 1
		} else {
			backlog = value
		}
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
