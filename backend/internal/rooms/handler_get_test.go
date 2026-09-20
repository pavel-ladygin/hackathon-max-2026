package rooms

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
)

func TestGetRoomRejectsUnauthenticatedAndInvalidRoomID(t *testing.T) {
	svc := NewService(nil)
	router := chi.NewRouter()
	router.Get("/api/v1/rooms/{roomId}", svc.GetRoom)

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/rooms/"+uuid.NewString(), nil))
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body.String(), `"code":"UNAUTHENTICATED"`) {
		t.Fatalf("unauthenticated response = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	invalid := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rooms/not-a-uuid", nil)
	req = req.WithContext(contracts.WithPrincipal(req.Context(), contracts.Principal{UserID: uuid.New()}))
	router.ServeHTTP(invalid, req)
	if invalid.Code != http.StatusNotFound || !strings.Contains(invalid.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("invalid UUID response = %d %s", invalid.Code, invalid.Body.String())
	}
	if got := invalid.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q; want no-store", got)
	}
}
