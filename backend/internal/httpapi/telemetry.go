package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// operationalTelemetry logs only a small set of critical operation labels.
// It deliberately excludes request paths, query strings, headers and bodies so
// invite tokens and user-supplied content cannot enter operational logs.
func operationalTelemetry(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			observed := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(observed, r)
			routeContext := chi.RouteContext(r.Context())
			if routeContext == nil {
				return
			}
			operation := criticalOperation(r.Method, routeContext.RoutePattern())
			if operation == "" {
				return
			}
			status := observed.status
			if status == 0 {
				status = http.StatusOK
			}
			attrs := []any{"operation", operation, "status", status, "duration_ms", time.Since(started).Milliseconds(), "request_id", RequestID(r.Context())}
			if status >= http.StatusInternalServerError {
				logger.Error("critical API operation completed", attrs...)
			} else if status >= http.StatusBadRequest {
				logger.Warn("critical API operation completed", attrs...)
			} else {
				logger.Info("critical API operation completed", attrs...)
			}
		})
	}
}

func criticalOperation(method, route string) string {
	switch {
	case method == http.MethodGet && route == "/api/v1/feed/home":
		return "feed_load"
	case method == http.MethodPost && route == "/api/v1/rooms":
		return "room_create"
	case method == http.MethodPost && route == "/api/v1/room-invites/{token}/join":
		return "room_join"
	case method == http.MethodPut && route == "/api/v1/rooms/{roomId}/events/{eventId}/vote":
		return "room_vote"
	default:
		return ""
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
