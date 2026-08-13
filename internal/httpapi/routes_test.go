package httpapi

import (
	"fmt"
	"testing"
)

func TestRoutePatternReturnsOnlyClosedTemplates(t *testing.T) {
	tests := map[string]string{
		"/v1/work-items":                           "/v1/work-items",
		"/v1/me/work-items":                        "/v1/me/work-items",
		"/v1/work-items/item-id":                   "/v1/work-items/{id}",
		"/v1/work-items/item-id/assignment":        "/v1/work-items/{id}/assignment",
		"/v1/work-items/item-id/start":             "/v1/work-items/{id}/start",
		"/v1/work-items/item-id/complete":          "/v1/work-items/{id}/complete",
		"/v1/work-items/item-id/history":           "/v1/work-items/{id}/history",
		"/v1/notifications":                        "/v1/notifications",
		"/v1/notifications/notification-id":        "/v1/notifications/{id}",
		"/v1/notifications/notification-id/replay": "/v1/notifications/{id}/replay",
		"/v1/work-items/item-id/delete":            "/unknown",
		"/health/live":                             "/unknown",
	}
	for path, want := range tests {
		if got := routePattern(path); got != want {
			t.Errorf("routePattern(%q)=%q want %q", path, got, want)
		}
	}
}

func TestRoutePatternMapsUnknownActionsToUnknown(t *testing.T) {
	paths := []string{
		"", "/", "/health/live", "/health/ready", "/metrics", "/unrelated",
		"/v1/work-items/", "/v1/work-items//assignment", "/v1/work-items/item-id/",
		"/v1/work-items/item-id/delete", "/v1/work-items/item-id/start/extra",
		"/v1/work-items/item-id/item-id/start", "/v1/notifications/",
		"/v1/notifications//replay", "/v1/notifications/notification-id/",
		"/v1/notifications/notification-id/retry", "/v1/notifications/notification-id/replay/extra",
	}
	for _, path := range paths {
		if got := routePattern(path); got != "/unknown" {
			t.Errorf("routePattern(%q)=%q", path, got)
		}
	}
}

func TestRoutePatternBoundsThousandsOfUniqueActions(t *testing.T) {
	labels := map[string]struct{}{}
	for index := 0; index < 10_000; index++ {
		labels[routePattern(fmt.Sprintf("/v1/work-items/item-id/action-%d", index))] = struct{}{}
	}
	if len(labels) != 1 {
		t.Fatalf("labels=%d", len(labels))
	}
	if _, ok := labels["/unknown"]; !ok {
		t.Fatalf("labels=%v", labels)
	}
}
