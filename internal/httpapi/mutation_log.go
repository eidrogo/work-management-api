package httpapi

import (
	"net/http"

	"github.com/eidrogo/work-management-api/internal/platform/id"
)

const (
	mutationOperationCreate             = "create"
	mutationOperationAssignment         = "assignment"
	mutationOperationStart              = "start"
	mutationOperationComplete           = "complete"
	mutationOperationNotificationReplay = "notification_replay"
)

type mutationLogResult struct {
	actorID        string
	operation      string
	workItemID     string
	notificationID string
	idempotent     *bool
	err            error
}

func (server *Server) logMutation(r *http.Request, result mutationLogResult) {
	attrs := []any{
		"request_id", requestIDFrom(r.Context()),
		"actor_id", result.actorID,
		"operation", result.operation,
	}
	if result.err == nil {
		attrs = append(attrs, "outcome", "success")
		if result.workItemID != "" {
			attrs = append(attrs, "work_item_id", result.workItemID)
		}
		if result.notificationID != "" {
			attrs = append(attrs, "notification_id", result.notificationID)
		}
		if result.idempotent != nil {
			attrs = append(attrs, "idempotent", *result.idempotent)
		}
		server.logger.Info("api mutation", attrs...)
		return
	}

	attrs = append(attrs, "outcome", "failure")
	if id.IsUUID(result.workItemID) {
		attrs = append(attrs, "work_item_id", result.workItemID)
	}
	if id.IsUUID(result.notificationID) {
		attrs = append(attrs, "notification_id", result.notificationID)
	}
	status, body := mapError(result.err)
	attrs = append(attrs, "status", status, "error_code", body.Code)
	if status >= http.StatusInternalServerError {
		server.logger.Error("api mutation", attrs...)
		return
	}
	server.logger.Warn("api mutation", attrs...)
}
