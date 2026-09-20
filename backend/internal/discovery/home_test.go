package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
)

type homeSearchFunc func(context.Context, SearchFilter) (Page, error)

func (f homeSearchFunc) Search(ctx context.Context, filter SearchFilter) (Page, error) {
	return f(ctx, filter)
}

type fakeHomeCities struct {
	city  uuid.UUID
	err   error
	calls int
}

func (f *fakeHomeCities) UserCity(context.Context, uuid.UUID) (uuid.UUID, error) {
	f.calls++
	return f.city, f.err
}

type fakeHomePreferences struct {
	value preferences.Value
	found bool
	err   error
	calls int
}

func (f *fakeHomePreferences) Get(context.Context, uuid.UUID) (preferences.Value, bool, error) {
	f.calls++
	return f.value, f.found, f.err
}

func TestHomeUsesProfileCityAndPreferencesWithoutASecondRecommender(t *testing.T) {
	user, city := uuid.New(), uuid.New()
	var filters []SearchFilter
	searcher := homeSearchFunc(func(_ context.Context, filter SearchFilter) (Page, error) {
		filters = append(filters, filter)
		return Page{Items: []Card{{ID: uuid.New(), StartsAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}}}, nil
	})
	cities := &fakeHomeCities{city: city}
	prefs := &fakeHomePreferences{found: true, value: preferences.Value{InterestSlugs: []string{"concerts"}, BudgetMaxMinor: 1500, UsualDayTypes: []string{"weekend"}, UsualTimeSlots: []string{"evening"}}}
	service := NewHomeService(searcher, cities, prefs)
	service.now = func() time.Time { return time.Date(2026, 9, 20, 14, 0, 0, 0, time.UTC) }
	service.newID = func() uuid.UUID { return uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa") }

	feed, err := service.Home(context.Background(), HomeInput{UserID: user, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if cities.calls != 1 || prefs.calls != 1 || len(filters) != 3 {
		t.Fatalf("city=%d preferences=%d searches=%d", cities.calls, prefs.calls, len(filters))
	}
	if filters[0].CityID != city || filters[0].Limit != 1 || filters[1].Limit != 3 {
		t.Fatalf("base filters = %#v %#v", filters[0], filters[1])
	}
	forYou := filters[2]
	if len(forYou.CategorySlugs) != 1 || forYou.CategorySlugs[0] != "concerts" || forYou.PriceMaxMinor == nil || *forYou.PriceMaxMinor != 1500 || len(forYou.DayTypes) != 1 || len(forYou.TimeSlots) != 1 {
		t.Fatalf("for you filter = %#v", forYou)
	}
	if got := sectionTypes(feed.Sections); len(got) != 3 || got[0] != "hero" || got[1] != "popular" || got[2] != "for_you" {
		t.Fatalf("sections = %#v", got)
	}
}

func TestHomeNoProfileCityFailsAndMissingPreferencesOmitForYou(t *testing.T) {
	user, city := uuid.New(), uuid.New()
	t.Run("profile city absent", func(t *testing.T) {
		service := NewHomeService(homeSearchFunc(func(context.Context, SearchFilter) (Page, error) { t.Fatal("Search called"); return Page{}, nil }), &fakeHomeCities{err: pgx.ErrNoRows}, &fakeHomePreferences{})
		if _, err := service.Home(context.Background(), HomeInput{UserID: user}); !errors.Is(err, ErrInvalidHome) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("preferences absent", func(t *testing.T) {
		var filters []SearchFilter
		service := NewHomeService(homeSearchFunc(func(_ context.Context, filter SearchFilter) (Page, error) {
			filters = append(filters, filter)
			return Page{}, nil
		}), &fakeHomeCities{city: city}, &fakeHomePreferences{})
		feed, err := service.Home(context.Background(), HomeInput{UserID: user})
		if err != nil || len(filters) != 2 || len(feed.Sections) != 0 {
			t.Fatalf("feed=%#v searches=%d error=%v", feed, len(filters), err)
		}
	})
}

func TestHomeNearbySortsDeterministicallyBeforeTrimming(t *testing.T) {
	user, city := uuid.New(), uuid.New()
	first, second, third := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	service := NewHomeService(homeSearchFunc(func(_ context.Context, filter SearchFilter) (Page, error) {
		if filter.Location == nil {
			return Page{}, nil
		}
		return Page{Items: []Card{{ID: first, StartsAt: starts.Add(time.Hour), DistanceMeters: intPtr(1000)}, {ID: second, StartsAt: starts, DistanceMeters: intPtr(200)}, {ID: third, StartsAt: starts.Add(2 * time.Hour), DistanceMeters: intPtr(200)}}}, nil
	}), &fakeHomeCities{city: city}, &fakeHomePreferences{})
	feed, err := service.Home(context.Background(), HomeInput{UserID: user, Limit: 2, Location: &Location{Latitude: 55.75, Longitude: 37.61}})
	if err != nil {
		t.Fatal(err)
	}
	if got := sectionTypes(feed.Sections); len(got) != 1 || got[0] != "nearby" {
		t.Fatalf("sections = %#v", got)
	}
	items := feed.Sections[0].Items
	if len(items) != 2 || items[0].ID != second || items[1].ID != third {
		t.Fatalf("nearby = %#v", items)
	}
}

type fakeHomeProvider struct {
	input HomeInput
	feed  HomeFeed
	err   error
	calls int
}

func (f *fakeHomeProvider) Home(_ context.Context, input HomeInput) (HomeFeed, error) {
	f.calls++
	f.input = input
	return f.feed, f.err
}

func TestHomeHandlerProtectsPrincipalAndValidatesCoordinates(t *testing.T) {
	t.Run("missing principal", func(t *testing.T) {
		provider := &fakeHomeProvider{}
		res := serveHome(provider, nil, "/api/v1/feed/home")
		if res.Code != http.StatusUnauthorized || provider.calls != 0 || res.Header().Get("WWW-Authenticate") != "Bearer" || !homeErrorCode(t, res, "UNAUTHENTICATED") {
			t.Fatalf("status=%d calls=%d headers=%v body=%s", res.Code, provider.calls, res.Header(), res.Body.String())
		}
	})
	t.Run("unpaired coordinates", func(t *testing.T) {
		provider := &fakeHomeProvider{}
		res := serveHome(provider, ptrUUID(uuid.New()), "/api/v1/feed/home?lat=55.75")
		if res.Code != http.StatusBadRequest || provider.calls != 0 || !homeErrorCode(t, res, "VALIDATION_FAILED") {
			t.Fatalf("status=%d calls=%d body=%s", res.Code, provider.calls, res.Body.String())
		}
	})
	t.Run("paired coordinates are request only", func(t *testing.T) {
		user := uuid.New()
		provider := &fakeHomeProvider{feed: HomeFeed{ID: uuid.New(), GeneratedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), Sections: []HomeSection{}}}
		res := serveHome(provider, &user, "/api/v1/feed/home?lat=55.75&lng=37.61&limit=2")
		if res.Code != http.StatusOK || provider.calls != 1 || provider.input.UserID != user || provider.input.Location == nil || provider.input.Location.Latitude != 55.75 || provider.input.Location.Longitude != 37.61 || provider.input.Limit != 2 {
			t.Fatalf("status=%d provider=%#v", res.Code, provider)
		}
	})
	t.Run("coordinates are optional", func(t *testing.T) {
		user := uuid.New()
		provider := &fakeHomeProvider{feed: HomeFeed{ID: uuid.New(), GeneratedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), Sections: []HomeSection{}}}
		res := serveHome(provider, &user, "/api/v1/feed/home")
		if res.Code != http.StatusOK || provider.calls != 1 || provider.input.Location != nil {
			t.Fatalf("status=%d provider=%#v", res.Code, provider)
		}
	})
}

func serveHome(provider *fakeHomeProvider, userID *uuid.UUID, target string) *httptest.ResponseRecorder {
	handler := NewHomeHandler(provider)
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) { r.Get("/api/v1/feed/home", handler.GetHome) })
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if userID != nil {
		req = req.WithContext(contracts.WithPrincipal(req.Context(), contracts.Principal{UserID: *userID}))
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

func sectionTypes(sections []HomeSection) []string {
	result := make([]string, len(sections))
	for i, section := range sections {
		result[i] = section.Type
	}
	return result
}
func ptrUUID(value uuid.UUID) *uuid.UUID { return &value }

func homeErrorCode(t *testing.T, res *httptest.ResponseRecorder, want string) bool {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(res.Body.Bytes(), &body) == nil && body.Error.Code == want
}
