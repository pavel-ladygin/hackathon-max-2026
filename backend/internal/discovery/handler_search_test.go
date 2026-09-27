package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"log/slog"
)

type fakeSearchProvider struct {
	filter       SearchFilter
	page         Page
	searchErr    error
	total        int
	decodeCursor Cursor
	decodeErr    error
	encoded      string
}

func (f *fakeSearchProvider) Search(_ context.Context, filter SearchFilter) (Page, error) {
	f.filter = filter
	return f.page, f.searchErr
}
func (f *fakeSearchProvider) Count(_ context.Context, filter SearchFilter) (int, error) {
	f.filter = filter
	return f.total, f.searchErr
}
func (f *fakeSearchProvider) DecodeCursor(string, SearchFilter) (Cursor, error) {
	return f.decodeCursor, f.decodeErr
}
func (f *fakeSearchProvider) EncodeNextCursor(Cursor) (string, error) {
	return f.encoded, nil
}

type fakeSearchCities struct {
	city uuid.UUID
	err  error
}

func (f *fakeSearchCities) UserCity(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.city, f.err
}

func TestSearchInputMapsFiltersAndUsesProfileCity(t *testing.T) {
	user, city := uuid.New(), uuid.New()
	service := &fakeSearchProvider{decodeCursor: Cursor{StartsAt: time.Now(), EventID: uuid.New(), FilterHash: "hash"}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/search?q=%D0%BC%D1%83%D0%B7%D1%8B%D0%BA%D0%B0&date_from=2026-09-20&date_to=2026-09-21&day_types=weekend,weekday&time_slots=evening,night&category_slugs=concerts,cinema&price_max_minor=0&distance_m=500&lat=55.75&lng=37.61&free_only=true&limit=2&cursor=signed", nil)
	filter, err := searchInput(context.Background(), req, user, &fakeSearchCities{city: city}, service)
	if err != nil {
		t.Fatal(err)
	}
	if filter.UserID != user || filter.CityID != city || filter.Query == nil || *filter.Query != "музыка" || filter.DateFrom.Format("2006-01-02") != "2026-09-20" || filter.DateTo.Format("2006-01-02") != "2026-09-21" || filter.PriceMaxMinor == nil || *filter.PriceMaxMinor != 0 || !filter.FreeOnly || filter.DistanceMeters == nil || *filter.DistanceMeters != 500 || filter.Location == nil || filter.Limit != 2 || filter.Cursor == nil || !filter.IncludeTotal || len(filter.GenreSlugs) != 1 || filter.GenreSlugs[0] != "concerts" {
		t.Fatalf("filter = %#v", filter)
	}
	if got := strings.Join(filter.DayTypes, ","); got != "weekday,weekend" {
		t.Fatalf("day types = %q", got)
	}
	if got := strings.Join(filter.TimeSlots, ","); got != "evening,night" {
		t.Fatalf("time slots = %q", got)
	}
}

func TestSearchInputRejectsInvalidParameters(t *testing.T) {
	city := uuid.New()
	cases := []string{
		"?city_id=not-a-uuid", "?date_from=2026-99-01", "?date_from=2026-09-21&date_to=2026-09-20",
		"?day_types=holiday", "?time_slots=lunch", "?category_slugs=music", "?price_max_minor=-1",
		"?distance_m=99&lat=55&lng=37", "?distance_m=500", "?lat=55", "?lat=100&lng=37",
		"?free_only=1", "?limit=0", "?limit=51", "?q=" + strings.Repeat("я", 121), "?cursor=bad",
	}
	for _, suffix := range cases {
		t.Run(suffix, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/events/search"+suffix, nil)
			_, err := searchInput(context.Background(), req, uuid.New(), &fakeSearchCities{city: city}, &fakeSearchProvider{decodeErr: ErrInvalidCursor})
			if !errors.Is(err, ErrInvalidFilter) && !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSearchHandlerReturnsPageAndMapsResponse(t *testing.T) {
	user, city, eventID := uuid.New(), uuid.New(), uuid.New()
	price, distance := 0, 700
	latitude, longitude := 55.75, 37.61
	next := Cursor{StartsAt: time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC), EventID: uuid.New(), FilterHash: "hash"}
	provider := &fakeSearchProvider{encoded: "next-signed", page: Page{Total: 1, NextCursor: &next, Items: []Card{{
		ID: eventID, Title: "Концерт", CategorySlug: "concerts", StartsAt: next.StartsAt, Timezone: "Europe/Moscow", DateLabel: "21 сентября", VenueName: "Клуб", Latitude: &latitude, Longitude: &longitude, PriceFromMinor: &price, Currency: "RUB", PriceLabel: "Бесплатно", DistanceMeters: &distance, Saved: true, Reasons: []Reason{{Code: "popular", Text: "Популярно"}},
	}}}}
	res := serveSearch(provider, &fakeSearchCities{city: city}, &user, "/api/v1/events/search?free_only=true")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	if provider.filter.CityID != city || !provider.filter.FreeOnly || provider.filter.Limit != defaultLimit {
		t.Fatalf("service filter = %#v", provider.filter)
	}
	var body struct {
		Items []struct {
			ID             uuid.UUID `json:"id"`
			PriceFromMinor *int      `json:"price_from_minor"`
			DistanceM      *int      `json:"distance_m"`
			Latitude       *float64  `json:"latitude"`
			Longitude      *float64  `json:"longitude"`
		} `json:"items"`
		AppliedFilters map[string]interface{} `json:"applied_filters"`
		TotalEstimate  *int                   `json:"total_estimate"`
		NextCursor     *string                `json:"next_cursor"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != eventID || body.Items[0].PriceFromMinor == nil || *body.Items[0].PriceFromMinor != 0 || body.Items[0].DistanceM == nil || *body.Items[0].DistanceM != distance || body.Items[0].Latitude == nil || *body.Items[0].Latitude != latitude || body.Items[0].Longitude == nil || *body.Items[0].Longitude != longitude || body.TotalEstimate == nil || *body.TotalEstimate != 1 || body.NextCursor == nil || *body.NextCursor != "next-signed" || body.AppliedFilters["city_id"] != city.String() || body.AppliedFilters["free_only"] != true {
		t.Fatalf("response = %#v", body)
	}
}

func TestSearchIncludeTotalFalseReturnsNullAndCountEndpointCounts(t *testing.T) {
	user, city := uuid.New(), uuid.New()
	provider := &fakeSearchProvider{total: 17}
	cities := &fakeSearchCities{city: city}
	res := serveSearch(provider, cities, &user, "/api/v1/events/search?include_total=false&q=%D1%84%D0%B8%D0%BB%D1%8C%D0%BC%D1%8B")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"total_estimate":null`) {
		t.Fatalf("search response = %d %s", res.Code, res.Body.String())
	}
	if provider.filter.IncludeTotal || len(provider.filter.GenreSlugs) != 1 || provider.filter.GenreSlugs[0] != "cinema" {
		t.Fatalf("search filter = %#v", provider.filter)
	}
	handler := NewSearchHandler(provider, cities)
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) { r.Get("/api/v1/events/search/count", handler.CountEvents) })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events/search/count?q=%D1%82%D0%B5%D0%B0%D1%82%D1%80%D0%B0", nil)
	request = request.WithContext(contracts.WithPrincipal(request.Context(), contracts.Principal{UserID: user}))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"total":17}`+"\n" || len(provider.filter.GenreSlugs) != 1 || provider.filter.GenreSlugs[0] != "theatre" {
		t.Fatalf("count response = %d %s, filter=%#v", recorder.Code, recorder.Body.String(), provider.filter)
	}
}

func TestGenreMappingRequiresGenreOnlyQuery(t *testing.T) {
	genre := "концерты"
	if got := genreSlugsForQuery(&genre); len(got) != 1 || got[0] != "concerts" {
		t.Fatalf("genre-only query = %v", got)
	}
	mixed := "концерты джаз"
	if got := genreSlugsForQuery(&mixed); len(got) != 0 {
		t.Fatalf("mixed query must not match every concert: %v", got)
	}
}

func TestCountRejectsPaginationParameters(t *testing.T) {
	user, city := uuid.New(), uuid.New()
	handler := NewSearchHandler(&fakeSearchProvider{}, &fakeSearchCities{city: city})
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) { r.Get("/api/v1/events/search/count", handler.CountEvents) })
	for _, suffix := range []string{"?cursor=old", "?limit=24", "?include_total=false"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/events/search/count"+suffix, nil)
		request = request.WithContext(contracts.WithPrincipal(request.Context(), contracts.Principal{UserID: user}))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", suffix, response.Code)
		}
	}
}

func TestSearchHandlerRejectsUnauthenticatedAndInvalidCursor(t *testing.T) {
	city := uuid.New()
	if res := serveSearch(&fakeSearchProvider{}, &fakeSearchCities{city: city}, nil, "/api/v1/events/search"); res.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", res.Code)
	}
	user := uuid.New()
	if res := serveSearch(&fakeSearchProvider{decodeErr: ErrInvalidCursor}, &fakeSearchCities{city: city}, &user, "/api/v1/events/search?cursor=tampered"); res.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status = %d", res.Code)
	}
	if res := serveSearch(&fakeSearchProvider{}, &fakeSearchCities{city: city}, &user, "/api/v1/events/search?cursor=one&cursor=two"); res.Code != http.StatusBadRequest {
		t.Fatalf("duplicate cursor status = %d", res.Code)
	}
}

func TestSearchHandlerReturnsValidationWhenProfileCityMissingAndEmptyResults(t *testing.T) {
	user := uuid.New()
	if res := serveSearch(&fakeSearchProvider{}, &fakeSearchCities{}, &user, "/api/v1/events/search"); res.Code != http.StatusBadRequest {
		t.Fatalf("missing city status = %d", res.Code)
	}
	res := serveSearch(&fakeSearchProvider{page: Page{}}, &fakeSearchCities{city: uuid.New()}, &user, "/api/v1/events/search")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "\"items\":[]") || !strings.Contains(res.Body.String(), "\"next_cursor\":null") {
		t.Fatalf("empty response = %d %s", res.Code, res.Body.String())
	}
}

func TestSearchHandlerKeepsDatabaseErrorsInternal(t *testing.T) {
	user := uuid.New()
	res := serveSearch(&fakeSearchProvider{}, &fakeSearchCities{err: errors.New("database unavailable")}, &user, "/api/v1/events/search")
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), `"code":"INTERNAL"`) || strings.Contains(res.Body.String(), "database unavailable") {
		t.Fatalf("response = %d %s", res.Code, res.Body.String())
	}
}

func serveSearch(provider *fakeSearchProvider, cities *fakeSearchCities, userID *uuid.UUID, target string) *httptest.ResponseRecorder {
	handler := NewSearchHandler(provider, cities)
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) { r.Get("/api/v1/events/search", handler.SearchEvents) })
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if userID != nil {
		req = req.WithContext(contracts.WithPrincipal(req.Context(), contracts.Principal{UserID: *userID}))
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}
