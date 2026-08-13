package httpapi

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/eidrogo/work-management-api/internal/auth"
)

type statusWriter struct {
	http.ResponseWriter
	status, bytes int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	count, err := w.ResponseWriter.Write(payload)
	w.bytes += count
	return count, err
}
func (server *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if !validRequestID(requestID) {
			generated, err := server.requestID()
			if err != nil {
				requestID = "request-id-unavailable"
			} else {
				requestID = generated
			}
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), requestID)))
	})
}
func validRequestID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && !strings.ContainsRune("._-", char) {
			return false
		}
	}
	return true
}
func (server *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				server.logger.Error("panic recovered", "request_id", requestIDFrom(r.Context()), "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
				server.writeError(w, r, &apiError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An internal error occurred.", Cause: fmt.Errorf("panic: %v", recovered)})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (server *Server) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		writer := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r)
		if writer.status == 0 {
			writer.status = http.StatusOK
		}
		duration := time.Since(started)
		if server.metrics != nil {
			route := "/unknown"
			if r.URL.EscapedPath() == r.URL.Path && canonicalAPIPath(r.URL.Path) {
				route = routePattern(r.URL.Path)
			}
			server.metrics.RecordHTTP(r.Method, route, writer.status, duration)
		}
		server.logger.Info("http request", "request_id", requestIDFrom(r.Context()), "method", r.Method, "path", r.URL.Path, "status", writer.status, "duration_ms", duration.Milliseconds())
	})
}
func (server *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		scheme, token, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
			if server.metrics != nil {
				server.metrics.RecordAuthFailure()
			}
			server.writeError(w, r, auth.ErrAuthenticationRequired)
			return
		}
		principal, err := server.authenticator.Authenticate(r.Context(), strings.TrimSpace(token))
		if err != nil {
			if server.metrics != nil {
				server.metrics.RecordAuthFailure()
			}
			server.writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
	})
}
