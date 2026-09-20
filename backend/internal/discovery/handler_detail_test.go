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
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"log/slog"
)

type fakeDetailProvider struct {
	userID, eventID uuid.UUID
	location        *Location
	detail          Detail
	err             error
	calls           int
}

func (f *fakeDetailProvider) Get(_ context.Context, userID, eventID uuid.UUID, location *Location) (Detail, error) {
	f.calls++
	f.userID, f.eventID, f.location = userID, eventID, location
	return f.detail, f.err
}

func TestDetailHandlerProtectsAndHidesInvalidOrUnknownIDs(t *testing.T) {
	t.Run("missing principal", func(t *testing.T) {
		provider := &fakeDetailProvider{}
		res := serveDetail(provider, nil, "/api/v1/events/"+uuid.New().String())
		if res.Code != http.StatusUnauthorized || provider.calls != 0 || res.Header().Get("WWW-Authenticate") != "Bearer" || !detailErrorCode(t, res, "UNAUTHENTICATED") {
			t.Fatalf("status=%d calls=%d headers=%v body=%s", res.Code, provider.calls, res.Header(), res.Body.String())
		}
	})
	for _, path := range []string{"not-a-uuid", uuid.Nil.String()} {
		t.Run(path, func(t *testing.T) {
			provider := &fakeDetailProvider{}
			res := serveDetail(provider, ptrUUID(uuid.New()), "/api/v1/events/"+path)
			if res.Code != http.StatusNotFound || provider.calls != 0 || !detailErrorCode(t, res, "NOT_FOUND") {
				t.Fatalf("status=%d calls=%d body=%s", res.Code, provider.calls, res.Body.String())
			}
		})
	}
	t.Run("unknown", func(t *testing.T) {
		provider := &fakeDetailProvider{err: pgx.ErrNoRows}
		res := serveDetail(provider, ptrUUID(uuid.New()), "/api/v1/events/"+uuid.New().String())
		if res.Code != http.StatusNotFound || provider.calls != 1 || !detailErrorCode(t, res, "NOT_FOUND") {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})
	t.Run("database failure", func(t *testing.T) {
		provider := &fakeDetailProvider{err: errors.New("database unavailable")}
		res := serveDetail(provider, ptrUUID(uuid.New()), "/api/v1/events/"+uuid.New().String())
		if res.Code != http.StatusInternalServerError || !detailErrorCode(t, res, "INTERNAL") {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})
}

func TestDetailHandlerMapsCompletePublicDetail(t *testing.T) {
	user, event, venue := uuid.New(), uuid.New(), uuid.New()
	subtitle, imageURL, metro, district, age := "Вечерняя программа", "https://cdn.example.test/card.jpg", "Тверская", "ЦАО", "16+"
	price, distance, width, height := 0, 734, 1280, 720
	endsAt := time.Date(2026, 10, 1, 22, 0, 0, 0, time.UTC)
	sourceUpdatedAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	provider := &fakeDetailProvider{detail: Detail{Card: Card{ID: event, Title: "Концерт", Subtitle: &subtitle, CategorySlug: "concerts", StartsAt: time.Date(2026, 10, 1, 19, 0, 0, 0, time.UTC), Timezone: "Europe/Moscow", DateLabel: "1 октября, 22:00", VenueName: "Клуб", DistanceMeters: &distance, DistanceLabel: stringPointer("734 м"), PriceFromMinor: &price, Currency: "RUB", PriceLabel: "Бесплатно", ImageURL: &imageURL, Saved: true, Reasons: []Reason{{Code: "popular", Text: "Популярно"}}}, Description: "Полное описание", EndsAt: &endsAt, Venue: Venue{ID: venue, Name: "Клуб", Address: "Москва, ул. Пример, 1", Latitude: 55.751, Longitude: 37.617, Metro: &metro, District: &district}, Images: []Image{{URL: "https://cdn.example.test/hero.jpg", Width: &width, Height: &height, Role: "hero"}, {URL: "https://cdn.example.test/gallery.jpg", Role: "gallery"}}, TicketAvailable: true, Status: "published", AgeRating: &age, Provenance: Provenance{Source: "demo", SourceUpdatedAt: &sourceUpdatedAt, IsDemo: true}}}
	res := serveDetail(provider, &user, "/api/v1/events/"+event.String()+"?lat=55&lng=37")
	if res.Code != http.StatusOK || provider.calls != 1 || provider.userID != user || provider.eventID != event || provider.location != nil {
		t.Fatalf("status=%d provider=%#v body=%s", res.Code, provider, res.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != event.String() || body["description"] != "Полное описание" || body["status"] != "published" || body["ticket_available"] != true || body["age_rating"] != age || body["ends_at"] != endsAt.Format(time.RFC3339) {
		t.Fatalf("response = %#v", body)
	}
	if body["price_from_minor"] != float64(price) || body["image_url"] != imageURL || body["distance_m"] != float64(distance) {
		t.Fatalf("card fields = %#v", body)
	}
	venueBody := body["venue"].(map[string]any)
	if venueBody["id"] != venue.String() || venueBody["metro"] != metro || venueBody["district"] != district {
		t.Fatalf("venue = %#v", venueBody)
	}
	provenance := body["data_provenance"].(map[string]any)
	if provenance["source"] != "demo" || provenance["is_demo"] != true || provenance["source_updated_at"] != sourceUpdatedAt.Format(time.RFC3339) {
		t.Fatalf("provenance = %#v", provenance)
	}
	images := body["images"].([]any)
	if len(images) != 2 || images[0].(map[string]any)["width"] != float64(width) || images[0].(map[string]any)["height"] != float64(height) || images[1].(map[string]any)["width"] != nil || images[1].(map[string]any)["height"] != nil {
		t.Fatalf("images = %#v", images)
	}
	for _, forbidden := range []string{"ticket_url", "max_id", "max_user_id", "preferences", "room", "private"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("response contains %q: %#v", forbidden, body)
		}
	}
}

func TestDetailResponseKeepsNullableFieldsAndAllowedStatuses(t *testing.T) {
	for _, status := range []string{"published", "sold_out", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			response := detailResponse(Detail{Card: Card{ID: uuid.New(), StartsAt: time.Now(), Reasons: []Reason{}}, Venue: Venue{ID: uuid.New()}, Images: []Image{}, Status: status, Provenance: Provenance{}})
			body, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded["status"] != status || decoded["price_from_minor"] != nil || decoded["image_url"] != nil || decoded["ends_at"] != nil || decoded["age_rating"] != nil || decoded["venue"].(map[string]any)["metro"] != nil || decoded["venue"].(map[string]any)["district"] != nil || decoded["data_provenance"].(map[string]any)["source_updated_at"] != nil {
				t.Fatalf("response = %#v", decoded)
			}
		})
	}
}

func serveDetail(provider *fakeDetailProvider, userID *uuid.UUID, target string) *httptest.ResponseRecorder {
	handler := NewDetailHandler(provider)
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) { r.Get("/api/v1/events/{eventId}", handler.GetEvent) })
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if userID != nil {
		req = req.WithContext(contracts.WithPrincipal(req.Context(), contracts.Principal{UserID: *userID}))
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

func detailErrorCode(t *testing.T, res *httptest.ResponseRecorder, want string) bool {
	t.Helper()
	return strings.Contains(res.Body.String(), `"code":"`+want+`"`)
}

func stringPointer(value string) *string { return &value }
