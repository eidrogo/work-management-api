package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type Server struct {
	logger        Logger
	authenticator Authenticator
	workItems     WorkItemApplication
	notifications NotificationApplication
	readiness     func(context.Context) error
	requestID     func() (string, error)
	maxBodyBytes  int64
	metrics       MetricRecorder
	handler       http.Handler
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.Logger == nil || config.Authenticator == nil || config.WorkItems == nil || config.Notifications == nil || config.Readiness == nil || config.RequestID == nil || config.MaxBodyBytes < 1 {
		return nil, fmt.Errorf("invalid HTTP server configuration")
	}
	server := &Server{logger: config.Logger, authenticator: config.Authenticator, workItems: config.WorkItems, notifications: config.Notifications, readiness: config.Readiness, requestID: config.RequestID, maxBodyBytes: config.MaxBodyBytes, metrics: config.MetricRecorder}
	root := http.NewServeMux()
	api := http.NewServeMux()
	api.HandleFunc("/v1/work-items", server.handleWorkItems)
	api.HandleFunc("/v1/work-items/", server.handleWorkItem)
	api.HandleFunc("/v1/me/work-items", server.handleOwnWorkItems)
	api.HandleFunc("/v1/notifications", server.handleNotifications)
	api.HandleFunc("/v1/notifications/", server.handleNotification)
	api.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) { server.writeError(w, r, notFound()) })
	root.Handle("/v1/", server.authenticate(api))
	root.HandleFunc("/health/live", server.handleLiveness)
	root.HandleFunc("/health/ready", server.handleReadiness)
	if config.Metrics != nil {
		root.Handle("/metrics", config.Metrics)
	} else {
		root.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("# metrics not configured\n")) })
	}
	root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { server.writeError(w, r, notFound()) })
	server.handler = server.requestIDMiddleware(server.recoveryMiddleware(server.accessLogMiddleware(root)))
	return server, nil
}
func (server *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server.handler.ServeHTTP(w, r)
}
func (server *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.writeError(w, r, methodNotAllowed(http.MethodGet))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (server *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		server.writeError(w, r, methodNotAllowed(http.MethodGet))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := server.readiness(ctx); err != nil {
		server.writeError(w, r, &apiError{Status: http.StatusServiceUnavailable, Code: "DEPENDENCY_UNAVAILABLE", Message: "A required dependency is unavailable.", Cause: err})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
