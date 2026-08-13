package httpapi

import (
	"net/http"
	"strings"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

func (server *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.writeError(w, r, methodNotAllowed(http.MethodGet))
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		server.writeError(w, r, err)
		return
	}
	actor, _ := principalFrom(r.Context())
	page, err := server.notifications.List(r.Context(), actor, notifications.ListInput{Status: notifications.Status(r.URL.Query().Get("status")), WorkItemID: r.URL.Query().Get("work_item_id"), Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		server.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Items, "page": map[string]string{"next_cursor": page.NextCursor}})
}
func (server *Server) handleNotification(w http.ResponseWriter, r *http.Request) {
	remainder := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/notifications/"), "/")
	segments := strings.Split(remainder, "/")
	if len(segments) == 0 || segments[0] == "" {
		server.writeError(w, r, notFound())
		return
	}
	id := segments[0]
	actor, _ := principalFrom(r.Context())
	if len(segments) == 1 {
		if r.Method != http.MethodGet {
			server.writeError(w, r, methodNotAllowed(http.MethodGet))
			return
		}
		delivery, err := server.notifications.Get(r.Context(), actor, id)
		if err != nil {
			server.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": delivery})
		return
	}
	if len(segments) == 2 && segments[1] == "replay" {
		if r.Method != http.MethodPost {
			server.writeError(w, r, methodNotAllowed(http.MethodPost))
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if err := server.decodeJSON(w, r, &body); err != nil {
			server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationNotificationReplay, notificationID: id, err: err})
			server.writeError(w, r, err)
			return
		}
		delivery, err := server.notifications.Replay(r.Context(), actor, id, notifications.ReplayInput{Reason: body.Reason, RequestID: requestIDFrom(r.Context())})
		if err != nil {
			server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationNotificationReplay, notificationID: id, err: err})
			server.writeError(w, r, err)
			return
		}
		server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationNotificationReplay, workItemID: delivery.WorkItemID, notificationID: id})
		if server.metrics != nil {
			server.metrics.RecordMutation(mutationOperationNotificationReplay, "success")
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"data": delivery})
		return
	}
	server.writeError(w, r, notFound())
}
