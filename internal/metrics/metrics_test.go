package metrics

import (
	"context"
	"net/http/httptest"
	"strings"
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

type fakeBacklog struct{ value notifications.Backlog }

func (reader *fakeBacklog) Backlog(context.Context) (notifications.Backlog, error) {
	return reader.value, nil
}
