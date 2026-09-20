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

func TestVoteHandlerRejectsUnauthenticatedAndMalformedRequests(t *testing.T) {
	svc := NewService(nil)
	router := chi.NewRouter()
	router.Put("/api/v1/rooms/{roomId}/events/{eventId}/vote", svc.VoteForRoomEvent)
	path := "/api/v1/rooms/" + uuid.NewString() + "/events/" + uuid.NewString() + "/vote"

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"pool_version":1,"vote":"like"}`)))
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body.String(), `"code":"UNAUTHENTICATED"`) {
		t.Fatalf("unauthenticated response = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	for name, body := range map[string]string{
		"missing field":   `{"pool_version":1}`,
		"unknown field":   `{"pool_version":1,"vote":"like","cursor":"x"}`,
		"invalid vote":    `{"pool_version":1,"vote":"maybe"}`,
		"invalid version": `{"pool_version":0,"vote":"like"}`,
		"trailing json":   `{"pool_version":1,"vote":"like"}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
			request = request.WithContext(contracts.WithPrincipal(request.Context(), contracts.Principal{UserID: uuid.New()}))
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"VALIDATION_FAILED"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestVoteHandlerHidesInvalidIdentifiersAsNotFound(t *testing.T) {
	svc := NewService(nil)
	router := chi.NewRouter()
	router.Put("/api/v1/rooms/{roomId}/events/{eventId}/vote", svc.VoteForRoomEvent)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/rooms/nope/events/nope/vote", strings.NewReader(`{"pool_version":1,"vote":"like"}`))
	request = request.WithContext(contracts.WithPrincipal(request.Context(), contracts.Principal{UserID: uuid.New()}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
