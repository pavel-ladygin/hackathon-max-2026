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

func TestGetRoomEventsRejectsUnauthenticatedAndInvalidInput(t *testing.T) {
	svc := NewService(nil)
	router := chi.NewRouter()
	router.Get("/api/v1/rooms/{roomId}/events", svc.GetRoomEvents)

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/rooms/"+uuid.NewString()+"/events", nil))
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body.String(), `"code":"UNAUTHENTICATED"`) {
		t.Fatalf("unauthenticated response = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	for _, path := range []string{"/api/v1/rooms/not-a-uuid/events", "/api/v1/rooms/" + uuid.NewString() + "/events?limit=51"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = request.WithContext(contracts.WithPrincipal(request.Context(), contracts.Principal{UserID: uuid.New()}))
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound && response.Code != http.StatusBadRequest {
			t.Fatalf("%s response = %d %s", path, response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s Cache-Control = %q", path, response.Header().Get("Cache-Control"))
		}
	}
}

func TestRoomEventsCursorIsScopedAndTamperProof(t *testing.T) {
	codec, err := NewRoomEventsCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	want := roomEventsCursor{RoomID: uuid.New(), PoolVersion: 2, UserID: uuid.New(), Position: 7}
	encoded, err := codec.Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(encoded, want)
	if err != nil || got != want {
		t.Fatalf("Decode() = %+v, %v; want %+v", got, err, want)
	}
	for name, value := range map[string]struct {
		value    string
		expected roomEventsCursor
	}{
		"tampered":   {encoded + "x", want},
		"other room": {encoded, roomEventsCursor{RoomID: uuid.New(), PoolVersion: want.PoolVersion, UserID: want.UserID}},
		"other user": {encoded, roomEventsCursor{RoomID: want.RoomID, PoolVersion: want.PoolVersion, UserID: uuid.New()}},
		"stale pool": {encoded, roomEventsCursor{RoomID: want.RoomID, PoolVersion: want.PoolVersion + 1, UserID: want.UserID}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Decode(value.value, value.expected); err != ErrInvalidRoomEventsCursor {
				t.Fatalf("Decode() error = %v, want ErrInvalidRoomEventsCursor", err)
			}
		})
	}
}

func TestRoomEventsInputValidation(t *testing.T) {
	for _, test := range []struct {
		query string
		limit int
		ok    bool
	}{
		{"", 0, true},
		{"?limit=1", 1, true},
		{"?limit=50&cursor=opaque", 50, true},
		{"?limit=0", 0, false},
		{"?limit=51", 0, false},
		{"?limit=x", 0, false},
		{"?limit=1&limit=2", 0, false},
		{"?cursor=", 0, false},
		{"?unknown=1", 0, false},
	} {
		r := httptest.NewRequest("GET", "/api/v1/rooms/id/events"+test.query, nil)
		input, err := roomEventsInput(r)
		if (err == nil) != test.ok || test.ok && input.Limit != test.limit {
			t.Errorf("query %q: input=%+v err=%v", test.query, input, err)
		}
	}
}

func TestRoomEventAvailabilityUsesBothParticipantsHardBudget(t *testing.T) {
	url := "https://tickets.example/event"
	price := int32(1500)
	available := contracts.Availability{Exists: true, Status: "published", TicketAvailable: true, TicketURL: &url, PriceFromMinor: &price}
	if !roomEventAvailable(available, 1500) {
		t.Fatal("inclusive budget boundary must be available")
	}
	if roomEventAvailable(available, 1499) {
		t.Fatal("price above the strictest participant budget must be unavailable")
	}
	for name, mutate := range map[string]func(*contracts.Availability){
		"missing":   func(a *contracts.Availability) { a.Exists = false },
		"cancelled": func(a *contracts.Availability) { a.Status = "cancelled" },
		"sold out":  func(a *contracts.Availability) { a.Status = "sold_out" },
		"no ticket": func(a *contracts.Availability) { a.TicketAvailable = false },
		"no url":    func(a *contracts.Availability) { a.TicketURL = nil },
		"no price":  func(a *contracts.Availability) { a.PriceFromMinor = nil },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := available
			mutate(&candidate)
			if roomEventAvailable(candidate, 2000) {
				t.Fatal("unavailable catalog state was accepted")
			}
		})
	}
}

func TestRoomRecommendationReasonsAreSafeAndCapped(t *testing.T) {
	raw := []byte(`[{"code":"time_fit","text":"Подходит по времени"},{"code":"private","text":"secret"},{"code":"budget_fit","text":"В бюджете"},{"code":"nearby","text":"Рядом"},{"code":"popular","text":"Популярно"}]`)
	reasons := roomRecommendationReasons(raw)
	if len(reasons) != 3 || string(reasons[0].Code) != "time_fit" || string(reasons[1].Code) != "budget_fit" || string(reasons[2].Code) != "nearby" {
		t.Fatalf("unsafe or uncapped reasons: %+v", reasons)
	}
	if reasons := roomRecommendationReasons([]byte(`{"unexpected":true}`)); len(reasons) != 0 {
		t.Fatalf("malformed reasons leaked: %+v", reasons)
	}
}
