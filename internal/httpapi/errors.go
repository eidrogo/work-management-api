package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/eidrogo/work-management-api/internal/auth"
	"github.com/eidrogo/work-management-api/internal/notifications"
	"github.com/eidrogo/work-management-api/internal/workitems"
)

type errorEnvelope struct {
	Error errorBody `json:"error"`
}
type errorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id"`
}
type apiError struct {
	Status        int
	Code, Message string
	Details       map[string]any
	Cause         error
}

func (e *apiError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Message
}
func (e *apiError) Unwrap() error { return e.Cause }
func invalid(message string, details map[string]any) error {
	return &apiError{Status: http.StatusBadRequest, Code: "INVALID_INPUT", Message: message, Details: details}
}
func methodNotAllowed(allowed string) error {
	return &apiError{Status: http.StatusMethodNotAllowed, Code: "METHOD_NOT_ALLOWED", Message: "The HTTP method is not allowed for this resource.", Details: map[string]any{"allowed": allowed}}
}
func notFound() error {
	return &apiError{Status: http.StatusNotFound, Code: "RESOURCE_NOT_FOUND", Message: "The requested resource was not found."}
}
func (server *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, workitems.ErrPermissionDenied) && server.metrics != nil {
		server.metrics.RecordAuthorizationFailure()
	}
	status, body := mapError(err)
	body.RequestID = requestIDFrom(r.Context())
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="work-management-api"`)
	}
	if status >= 500 {
		server.logger.Error("request failed", "request_id", body.RequestID, "method", r.Method, "path", r.URL.Path, "error", err)
	}
	writeJSON(w, status, errorEnvelope{Error: body})
}
func mapError(err error) (int, errorBody) {
	var api *apiError
	if errors.As(err, &api) {
		return api.Status, errorBody{Code: api.Code, Message: api.Message, Details: api.Details}
	}
	switch {
	case errors.Is(err, auth.ErrAuthenticationRequired), errors.Is(err, auth.ErrInvalidCredentials):
		return http.StatusUnauthorized, errorBody{Code: "AUTHENTICATION_REQUIRED", Message: "A valid bearer token is required."}
	case errors.Is(err, auth.ErrAuthenticationUnavailable):
		return http.StatusServiceUnavailable, errorBody{Code: "AUTHENTICATION_UNAVAILABLE", Message: "Authentication is temporarily unavailable."}
	case errors.Is(err, workitems.ErrPermissionDenied):
		return http.StatusForbidden, errorBody{Code: "PERMISSION_DENIED", Message: "You are not allowed to perform this action."}
	case errors.Is(err, workitems.ErrResourceNotFound), errors.Is(err, notifications.ErrResourceNotFound):
		return http.StatusNotFound, errorBody{Code: "RESOURCE_NOT_FOUND", Message: "The requested resource was not found."}
	case errors.Is(err, workitems.ErrInvalidInput), errors.Is(err, workitems.ErrIdempotencyKeyRequired), errors.Is(err, workitems.ErrExpectedVersionRequired), errors.Is(err, notifications.ErrInvalidInput):
		code, message, details := workitems.ErrorContract(err)
		if code == "INTERNAL_ERROR" {
			code, message = "INVALID_INPUT", "The request contains invalid input."
		}
		return http.StatusBadRequest, errorBody{Code: code, Message: message, Details: details}
	case errors.Is(err, workitems.ErrAssigneeMustBeWorker), errors.Is(err, workitems.ErrReassignmentReasonRequired):
		code, message, details := workitems.ErrorContract(err)
		return http.StatusUnprocessableEntity, errorBody{Code: code, Message: message, Details: details}
	case errors.Is(err, workitems.ErrInvalidTransition), errors.Is(err, workitems.ErrAssignmentConflict), errors.Is(err, workitems.ErrConcurrentModification), errors.Is(err, workitems.ErrCompletedImmutable), errors.Is(err, workitems.ErrIdempotencyKeyReused), errors.Is(err, notifications.ErrNotReplayable), errors.Is(err, notifications.ErrLeaseLost):
		code, message, details := workitems.ErrorContract(err)
		if code == "INTERNAL_ERROR" {
			if errors.Is(err, notifications.ErrNotReplayable) {
				code, message = "NOTIFICATION_NOT_RETRYABLE", "Only a terminally failed notification can be replayed."
			} else {
				code, message = "NOTIFICATION_LEASE_LOST", "The notification is no longer owned by this worker."
			}
		}
		return http.StatusConflict, errorBody{Code: code, Message: message, Details: details}
	default:
		return http.StatusInternalServerError, errorBody{Code: "INTERNAL_ERROR", Message: "An internal error occurred."}
	}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
