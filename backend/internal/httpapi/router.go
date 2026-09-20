// Package httpapi provides the HTTP transport foundation.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const requestIDHeader = "X-Request-ID"

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 32 * 1024
)

// Readiness checks the dependencies required to accept traffic.
type Readiness interface {
	Ready(context.Context) error
}

// NewRouter creates the shared API router. Feature routes are registered by their owners.
func NewRouter(readiness Readiness, logger *slog.Logger, registerRoutes ...func(chi.Router)) http.Handler {
	r := chi.NewRouter()
	r.Use(requestID, recovery(logger))
	r.Get("/api/v1/health/ready", readyHandler(readiness))
	for _, register := range registerRoutes {
		register(r)
	}
	return r
}

// NewServer creates an HTTP server with bounded transport timeouts.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("http handler panic", "request_id", RequestID(r.Context()))
					WriteError(w, RequestID(r.Context()), Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "Internal server error"})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func readyHandler(readiness Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := readiness.Ready(ctx); err != nil {
			WriteError(w, RequestID(r.Context()), Error{Status: http.StatusServiceUnavailable, Code: "INTERNAL", Message: "service unavailable"})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready", "database": "ready", "migrations": "current"})
	}
}

type requestIDKey struct{}

// RequestID returns the correlation ID assigned by the router.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
