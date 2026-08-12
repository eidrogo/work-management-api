package httpapi

import "strings"

func routePattern(path string) string {
	switch {
	case path == "/v1/work-items", path == "/v1/me/work-items", path == "/v1/notifications", path == "/health/live", path == "/health/ready", path == "/metrics":
		return path
	case strings.HasPrefix(path, "/v1/work-items/"):
		parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/v1/work-items/"), "/"), "/")
		if len(parts) == 1 {
			return "/v1/work-items/{id}"
		}
		if len(parts) == 2 {
			return "/v1/work-items/{id}/" + parts[1]
		}
		return "/v1/work-items/*"
	case strings.HasPrefix(path, "/v1/notifications/"):
		parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/v1/notifications/"), "/"), "/")
		if len(parts) == 1 {
			return "/v1/notifications/{id}"
		}
		if len(parts) == 2 {
			return "/v1/notifications/{id}/" + parts[1]
		}
		return "/v1/notifications/*"
	default:
		return "/unknown"
	}
}
