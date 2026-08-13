package httpapi

import (
	"fmt"
	"net/http"
	"strings"
)

type Server struct {
	logger        Logger
	authenticator Authenticator
	workItems     WorkItemApplication
	notifications NotificationApplication
	requestID     func() (string, error)
	maxBodyBytes  int64
	metrics       MetricRecorder
	handler       http.Handler
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.Logger == nil || config.Authenticator == nil || config.WorkItems == nil || config.Notifications == nil || config.RequestID == nil || config.MaxBodyBytes < 1 {
		return nil, fmt.Errorf("invalid HTTP server configuration")
	}
	server := &Server{logger: config.Logger, authenticator: config.Authenticator, workItems: config.WorkItems, notifications: config.Notifications, requestID: config.RequestID, maxBodyBytes: config.MaxBodyBytes, metrics: config.MetricRecorder}
	root := http.NewServeMux()
	api := http.NewServeMux()
	api.HandleFunc("/v1/work-items", server.handleWorkItems)
	api.HandleFunc("/v1/work-items/", server.handleWorkItem)
	api.HandleFunc("/v1/me/work-items", server.handleOwnWorkItems)
	api.HandleFunc("/v1/notifications", server.handleNotifications)
	api.HandleFunc("/v1/notifications/", server.handleNotification)
	api.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) { server.writeError(w, r, notFound()) })
	exactBusinessRoutes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			server.writeError(w, r, notFound())
			return
		}
		api.ServeHTTP(w, r)
	})
	root.Handle("/v1/", server.authenticate(exactBusinessRoutes))
	root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { server.writeError(w, r, notFound()) })
	server.handler = server.requestIDMiddleware(server.recoveryMiddleware(server.accessLogMiddleware(root)))
	return server, nil
}
func (server *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server.handler.ServeHTTP(w, r)
}
