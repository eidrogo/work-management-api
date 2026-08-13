package httpapi

import "strings"

func routePattern(path string) string {
	switch path {
	case "/v1/work-items", "/v1/me/work-items", "/v1/notifications":
		return path
	}
	parts := strings.Split(path, "/")
	if len(parts) == 4 && parts[0] == "" && parts[1] == "v1" && parts[2] == "work-items" && parts[3] != "" {
		return "/v1/work-items/{id}"
	}
	if len(parts) == 5 && parts[0] == "" && parts[1] == "v1" && parts[2] == "work-items" && parts[3] != "" {
		switch parts[4] {
		case "assignment":
			return "/v1/work-items/{id}/assignment"
		case "start":
			return "/v1/work-items/{id}/start"
		case "complete":
			return "/v1/work-items/{id}/complete"
		case "history":
			return "/v1/work-items/{id}/history"
		}
	}
	if len(parts) == 4 && parts[0] == "" && parts[1] == "v1" && parts[2] == "notifications" && parts[3] != "" {
		return "/v1/notifications/{id}"
	}
	if len(parts) == 5 && parts[0] == "" && parts[1] == "v1" && parts[2] == "notifications" && parts[3] != "" && parts[4] == "replay" {
		return "/v1/notifications/{id}/replay"
	}
	return "/unknown"
}
