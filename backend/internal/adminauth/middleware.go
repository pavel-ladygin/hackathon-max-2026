// Package adminauth authenticates requests forwarded by the trusted Nginx
// internal-admin proxy.
package adminauth

import (
	"context"
	"crypto/subtle"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"net/http"
	"strings"
	"unicode"
)

type actorContextKey struct{}

// User returns the verified Nginx Basic Auth username from the request context.
func User(r *http.Request) string {
	if r == nil {
		return ""
	}
	user, _ := r.Context().Value(actorContextKey{}).(string)
	return user
}

// NewMiddleware protects the versioned internal API namespace. An empty
// configured secret closes the namespace with 503 until deployment wiring is
// installed. The middleware should wrap the global API handler.
func NewMiddleware(secret string) func(http.Handler) http.Handler {
	secret = strings.TrimSpace(secret)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isInternalAPI(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if secret == "" {
				writeError(w, http.StatusServiceUnavailable, "internal_admin_unavailable", "Internal administration is not configured")
				return
			}
			values := r.Header.Values("X-Internal-Admin-Secret")
			if len(values) != 1 || subtle.ConstantTimeCompare([]byte(secret), []byte(values[0])) != 1 {
				writeError(w, http.StatusForbidden, "internal_admin_forbidden", "Internal administration access is required")
				return
			}
			users := r.Header.Values("X-Internal-Admin-User")
			if len(users) != 1 || !validUser(users[0]) {
				writeError(w, http.StatusForbidden, "internal_admin_forbidden", "Internal administration access is required")
				return
			}
			ctx := r.Context()
			r = r.WithContext(contextWithUser(ctx, strings.TrimSpace(users[0])))
			next.ServeHTTP(w, r)
		})
	}
}

func isInternalAPI(path string) bool {
	return path == "/api/v1/internal" || strings.HasPrefix(path, "/api/v1/internal/")
}

func validUser(user string) bool {
	user = strings.TrimSpace(user)
	if user == "" || len(user) > 128 {
		return false
	}
	for _, r := range user {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func contextWithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, actorContextKey{}, user)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	requestID := uuid.NewString()
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteError(w, requestID, httpapi.Error{Status: status, Code: code, Message: message})
}
