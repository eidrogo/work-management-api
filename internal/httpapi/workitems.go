package httpapi

import (
	"net/http"
	"strings"

	"github.com/eidrogo/work-management-api/internal/workitems"
)

func (server *Server) handleWorkItems(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		server.createWorkItem(w, r)
	case http.MethodGet:
		server.listManagerWorkItems(w, r)
	default:
		server.writeError(w, r, methodNotAllowed("GET, POST"))
	}
}
func (server *Server) handleOwnWorkItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.writeError(w, r, methodNotAllowed(http.MethodGet))
		return
	}
	actor, _ := principalFrom(r.Context())
	limit, err := parseLimit(r)
	if err != nil {
		server.writeError(w, r, err)
		return
	}
	page, err := server.workItems.ListOwn(r.Context(), actor, workitems.ListInput{Status: workitems.Status(r.URL.Query().Get("status")), Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		server.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Items, "page": map[string]string{"next_cursor": page.NextCursor}})
}
func (server *Server) handleWorkItem(w http.ResponseWriter, r *http.Request) {
	remainder := strings.TrimPrefix(r.URL.Path, "/v1/work-items/")
	segments := strings.Split(strings.Trim(remainder, "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		server.writeError(w, r, notFound())
		return
	}
	id := segments[0]
	if len(segments) == 1 {
		if r.Method != http.MethodGet {
			server.writeError(w, r, methodNotAllowed(http.MethodGet))
			return
		}
		server.getWorkItem(w, r, id)
		return
	}
	if len(segments) != 2 {
		server.writeError(w, r, notFound())
		return
	}
	switch segments[1] {
	case "assignment":
		if r.Method != http.MethodPut {
			server.writeError(w, r, methodNotAllowed(http.MethodPut))
			return
		}
		server.assignWorkItem(w, r, id)
	case "start":
		if r.Method != http.MethodPost {
			server.writeError(w, r, methodNotAllowed(http.MethodPost))
			return
		}
		server.transitionWorkItem(w, r, id, true)
	case "complete":
		if r.Method != http.MethodPost {
			server.writeError(w, r, methodNotAllowed(http.MethodPost))
			return
		}
		server.transitionWorkItem(w, r, id, false)
	case "history":
		if r.Method != http.MethodGet {
			server.writeError(w, r, methodNotAllowed(http.MethodGet))
			return
		}
		server.workItemHistory(w, r, id)
	default:
		server.writeError(w, r, notFound())
	}
}
func (server *Server) createWorkItem(w http.ResponseWriter, r *http.Request) {
	actor, _ := principalFrom(r.Context())
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := server.decodeJSON(w, r, &body); err != nil {
		server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationCreate, err: err})
		server.writeError(w, r, err)
		return
	}
	result, err := server.workItems.Create(r.Context(), actor, workitems.CreateInput{Title: body.Title, Description: body.Description, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationCreate, err: err})
		server.writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", quoteETag(result.Item.Version))
	status := http.StatusCreated
	if result.Idempotent {
		status = http.StatusOK
		w.Header().Set("Idempotent-Replayed", "true")
	}
	server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationCreate, workItemID: result.Item.ID, idempotent: &result.Idempotent})
	if server.metrics != nil {
		server.metrics.RecordMutation(mutationOperationCreate, "success")
	}
	writeJSON(w, status, map[string]any{"data": result.Item, "meta": map[string]bool{"idempotent": result.Idempotent}})
}
func (server *Server) listManagerWorkItems(w http.ResponseWriter, r *http.Request) {
	actor, _ := principalFrom(r.Context())
	limit, err := parseLimit(r)
	if err != nil {
		server.writeError(w, r, err)
		return
	}
	page, err := server.workItems.ListManager(r.Context(), actor, workitems.ListInput{Status: workitems.Status(r.URL.Query().Get("status")), AssigneeID: r.URL.Query().Get("assignee_id"), Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		server.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Items, "page": map[string]string{"next_cursor": page.NextCursor}})
}
func (server *Server) getWorkItem(w http.ResponseWriter, r *http.Request, id string) {
	actor, _ := principalFrom(r.Context())
	item, err := server.workItems.Get(r.Context(), actor, id)
	if err != nil {
		server.writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", quoteETag(item.Version))
	writeJSON(w, http.StatusOK, map[string]any{"data": item})
}
func (server *Server) assignWorkItem(w http.ResponseWriter, r *http.Request, id string) {
	actor, _ := principalFrom(r.Context())
	var body struct {
		AssigneeID string `json:"assignee_id"`
		Reason     string `json:"reason"`
	}
	if err := server.decodeJSON(w, r, &body); err != nil {
		server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationAssignment, workItemID: id, err: err})
		server.writeError(w, r, err)
		return
	}
	version, err := parseIfMatch(r)
	if err != nil {
		server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationAssignment, workItemID: id, err: err})
		server.writeError(w, r, err)
		return
	}
	result, err := server.workItems.Assign(r.Context(), actor, id, workitems.AssignmentInput{AssigneeID: body.AssigneeID, Reason: body.Reason, ExpectedVersion: version})
	if err != nil {
		server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationAssignment, workItemID: id, err: err})
		server.writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", quoteETag(result.Item.Version))
	if result.Idempotent {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: mutationOperationAssignment, workItemID: result.Item.ID, idempotent: &result.Idempotent})
	if server.metrics != nil {
		server.metrics.RecordMutation(mutationOperationAssignment, "success")
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result.Item, "meta": map[string]bool{"idempotent": result.Idempotent}})
}
func (server *Server) transitionWorkItem(w http.ResponseWriter, r *http.Request, id string, start bool) {
	actor, _ := principalFrom(r.Context())
	operation := mutationOperationComplete
	if start {
		operation = mutationOperationStart
	}
	version, err := parseIfMatch(r)
	if err != nil {
		server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: operation, workItemID: id, err: err})
		server.writeError(w, r, err)
		return
	}
	var result workitems.MutationResult
	if start {
		result, err = server.workItems.Start(r.Context(), actor, id, workitems.TransitionInput{ExpectedVersion: version})
	} else {
		result, err = server.workItems.Complete(r.Context(), actor, id, workitems.TransitionInput{ExpectedVersion: version})
	}
	if err != nil {
		server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: operation, workItemID: id, err: err})
		server.writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", quoteETag(result.Item.Version))
	if result.Idempotent {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	server.logMutation(r, mutationLogResult{actorID: actor.ID, operation: operation, workItemID: result.Item.ID, idempotent: &result.Idempotent})
	if server.metrics != nil {
		server.metrics.RecordMutation(operation, "success")
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result.Item, "meta": map[string]bool{"idempotent": result.Idempotent}})
}
func (server *Server) workItemHistory(w http.ResponseWriter, r *http.Request, id string) {
	limit, err := parseLimit(r)
	if err != nil {
		server.writeError(w, r, err)
		return
	}
	actor, _ := principalFrom(r.Context())
	page, err := server.workItems.History(r.Context(), actor, id, workitems.HistoryInput{Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		server.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": page.Items, "page": map[string]string{"next_cursor": page.NextCursor}})
}
