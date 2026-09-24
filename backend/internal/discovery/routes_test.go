package discovery

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
)

func TestDiscoveryHandlersRegisterAuthenticatedRoutes(t *testing.T) {
	user := uuid.New()
	authenticated := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: user})))
		})
	}
	search := &fakeSearchProvider{page: Page{}}
	home := &fakeHomeProvider{feed: HomeFeed{ID: uuid.New()}}
	detail := &fakeDetailProvider{detail: Detail{Card: Card{ID: user}, Venue: Venue{ID: uuid.New()}}}
	router := chi.NewRouter()
	NewHomeHandler(home).RegisterRoutes(router, authenticated)
	NewSearchHandler(search, &fakeSearchCities{city: uuid.New()}).RegisterRoutes(router, authenticated)
	NewDetailHandler(detail).RegisterRoutes(router, authenticated)

	for _, target := range []string{"/api/v1/feed/home", "/api/v1/events/search", "/api/v1/events/map?west=37&south=55&east=38&north=56&zoom=10", "/api/v1/events/" + uuid.New().String()} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code == http.StatusNotFound || res.Code == http.StatusUnauthorized {
			t.Fatalf("route %s was not registered/authenticated: status %d body %s", target, res.Code, res.Body.String())
		}
	}
}
