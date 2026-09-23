package timepad

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func TestRequestedOptionalFieldsNormalizeIntoProviderEvent(t *testing.T) {
	var event eventDTO
	raw := `{
		"id":42,
		"created_at":"2026-09-01T09:00:00+03:00",
		"starts_at":"2026-10-01T19:00:00+03:00",
		"ends_at":"2026-10-01T21:30:00+03:00",
		"name":"Event",
		"description_short":"Description",
		"url":"https://timepad.ru/event/42",
		"poster_image":{"default_url":"https://ucare.timepad.ru/poster/"},
		"location":{"city":"Москва","address":"","coordinates":[55.75,37.61]},
		"organization":{"id":7,"name":"Organizer"},
		"categories":[{"id":1,"name":"Концерты"}],
		"age_limit":"18+",
		"access_status":"public",
		"moderation_status":"featured",
		"registration_data":{"price_min":500,"price_max":1500,"is_registration_open":true}
	}`
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}

	normalized, ok := normalizeEvent(event)
	if !ok {
		t.Fatal("event with requested optional fields was rejected")
	}
	wantStart := time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 10, 1, 18, 30, 0, 0, time.UTC)
	wantPublished := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	if normalized.Description != "Description" || normalized.Subtitle == nil || *normalized.Subtitle != "Description" {
		t.Fatalf("description fields = %q, %v", normalized.Description, normalized.Subtitle)
	}
	if normalized.StartsAt != wantStart || normalized.EndsAt == nil || *normalized.EndsAt != wantEnd || normalized.PublishedAt == nil || *normalized.PublishedAt != wantPublished {
		t.Fatalf("normalized dates = start %v, end %v, published %v", normalized.StartsAt, normalized.EndsAt, normalized.PublishedAt)
	}
	if normalized.Venue.Name != "Organizer" || normalized.AgeRating == nil || *normalized.AgeRating != "18+" {
		t.Fatalf("venue/age = %+v, %v", normalized.Venue, normalized.AgeRating)
	}
	if normalized.PriceFromMinor == nil || *normalized.PriceFromMinor != 50_000 || normalized.PriceToMinor == nil || *normalized.PriceToMinor != 150_000 {
		t.Fatalf("prices = %v..%v", normalized.PriceFromMinor, normalized.PriceToMinor)
	}
	if len(normalized.Images) != 1 || len(normalized.Categories) != 1 || !normalized.Categories[0].IsPrimary {
		t.Fatalf("images/categories = %+v / %+v", normalized.Images, normalized.Categories)
	}
	if normalized.Status != providers.EventStatusPublished || !normalized.TicketAvailable || !normalized.ProviderActive {
		t.Fatalf("availability = status %q, ticket_available %t, provider_active %t", normalized.Status, normalized.TicketAvailable, normalized.ProviderActive)
	}
}

func TestNumericAgeLimitDecodesAndNormalizes(t *testing.T) {
	var event eventDTO
	raw := `{"id":42,"name":"Event","starts_at":"2026-10-01T19:00:00+03:00","age_limit":18}`
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	normalized, ok := normalizeEvent(event)
	if !ok {
		t.Fatal("event with numeric age_limit was rejected")
	}
	if normalized.AgeRating == nil || *normalized.AgeRating != "18+" {
		t.Fatalf("age rating = %v, want 18+", normalized.AgeRating)
	}
}

func TestNormalizeProviderLifecycle(t *testing.T) {
	tests := []struct {
		name         string
		access       string
		moderation   string
		registration bool
		wantActive   bool
		wantTickets  bool
	}{
		{name: "public open", access: "public", moderation: "featured", registration: true, wantActive: true, wantTickets: true},
		{name: "public registration closed", access: "public", moderation: "shown", wantActive: true},
		{name: "legacy missing statuses open", registration: true, wantActive: true, wantTickets: true},
		{name: "legacy missing statuses closed", wantActive: true},
		{name: "private", access: "private", registration: true, wantTickets: true},
		{name: "link only", access: "link_only", registration: true, wantTickets: true},
		{name: "hidden access", access: "hidden", registration: true, wantTickets: true},
		{name: "hidden moderation", access: "public", moderation: "hidden", registration: true, wantTickets: true},
		{name: "unknown access fails closed", access: "future_status", registration: true, wantTickets: true},
		{name: "unknown moderation fails closed", access: "public", moderation: "future_status", registration: true, wantTickets: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := eventDTO{
				ID: 42, Name: "Event", StartsAt: "2026-10-01T10:00:00+03:00", URL: "https://timepad.ru/event/42",
				AccessStatus: test.access, ModerationStatus: test.moderation,
				RegistrationData: registrationDataDTO{IsRegistrationOpen: test.registration},
			}
			normalized, ok := normalizeEvent(event)
			if !ok {
				t.Fatal("valid lifecycle fixture was rejected")
			}
			if normalized.Status != providers.EventStatusPublished {
				t.Fatalf("status = %q, want published", normalized.Status)
			}
			if normalized.ProviderActive != test.wantActive || normalized.TicketAvailable != test.wantTickets {
				t.Fatalf("provider_active/ticket_available = %t/%t, want %t/%t", normalized.ProviderActive, normalized.TicketAvailable, test.wantActive, test.wantTickets)
			}
		})
	}
}

func TestNormalizeInvalidCTAIsUnavailable(t *testing.T) {
	event := eventDTO{
		ID: 42, Name: "Event", StartsAt: "2026-10-01T10:00:00+03:00", URL: "https://attacker.example/event/42",
		AccessStatus: "public", ModerationStatus: "shown",
		RegistrationData: registrationDataDTO{IsRegistrationOpen: true},
	}
	normalized, ok := normalizeEvent(event)
	if !ok {
		t.Fatal("event with invalid CTA was rejected")
	}
	if normalized.TicketURL != nil || normalized.TicketAvailable || !normalized.ProviderActive || normalized.Status != providers.EventStatusPublished {
		t.Fatalf("unexpected lifecycle/CTA: %+v", normalized)
	}
}

func TestNormalizeVenuePrefersAddressOverOrganization(t *testing.T) {
	event := eventDTO{
		ID: 42, Name: "Event", StartsAt: "2026-10-01T10:00:00+03:00",
		Location: locationDTO{Address: "Venue address"}, Organization: organizationDTO{Name: "Organizer"},
	}
	normalized, ok := normalizeEvent(event)
	if !ok {
		t.Fatal("valid venue fixture was rejected")
	}
	if normalized.Venue.Name != "Venue address" || normalized.Venue.Address != "Venue address" {
		t.Fatalf("venue = %+v", normalized.Venue)
	}
}

func TestLocationCoordinatesDecodeNumericAndNumericStrings(t *testing.T) {
	for name, raw := range map[string]string{
		"numbers": `[55.751244,37.618423]`,
		"strings": `["55.751244","37.618423"]`,
	} {
		t.Run(name, func(t *testing.T) {
			var location locationDTO
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"coordinates":%s}`, raw)), &location); err != nil {
				t.Fatal(err)
			}
			if len(location.Coordinates) != 2 || location.Coordinates[0] != 55.751244 || location.Coordinates[1] != 37.618423 {
				t.Fatalf("coordinates = %#v", location.Coordinates)
			}
		})
	}
}

func TestLocationCoordinatesInvalidOrMissingNormalizeAsAbsent(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid string": `{"coordinates":["invalid","37.618423"]}`,
		"invalid type":   `{"coordinates":[true,37.618423]}`,
		"empty":          `{"coordinates":[]}`,
		"null":           `{"coordinates":null}`,
		"missing":        `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			var location locationDTO
			if err := json.Unmarshal([]byte(raw), &location); err != nil {
				t.Fatalf("invalid coordinates must not fail the page: %v", err)
			}
			event := eventDTO{ID: 1, Name: "Event", StartsAt: "2026-10-01T10:00:00+03:00", Location: locationDTO{Address: "Venue address", Coordinates: location.Coordinates}}
			normalized, ok := normalizeEvent(event)
			if !ok {
				t.Fatalf("coordinates %#v must not reject otherwise valid event", location.Coordinates)
			}
			if normalized.Venue.Latitude != nil || normalized.Venue.Longitude != nil || normalized.Venue.Address != "Venue address" || normalized.Venue.Name != "Venue address" {
				t.Fatalf("normalized venue = %#v", normalized.Venue)
			}
		})
	}
}

func TestInvalidCoordinatesDoNotFailWholePageDecode(t *testing.T) {
	var page eventsPage
	raw := `{"total":2,"values":[` +
		`{"id":1,"location":{"coordinates":["55.75","37.61"]}},` +
		`{"id":2,"location":{"coordinates":["invalid","37.61"]}}]}`
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Values) != 2 || len(page.Values[0].Location.Coordinates) != 2 || len(page.Values[1].Location.Coordinates) != 0 {
		t.Fatalf("decoded page = %#v", page.Values)
	}
}

func TestCategoriesDecodeArrayAndEmptyObject(t *testing.T) {
	for name, test := range map[string]struct {
		raw       string
		wantCount int
	}{
		"array":        {`[{"id":1,"name":"Концерты"}]`, 1},
		"empty array":  {`[]`, 0},
		"empty object": {`{}`, 0},
	} {
		t.Run(name, func(t *testing.T) {
			var event eventDTO
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"categories":%s}`, test.raw)), &event); err != nil {
				t.Fatal(err)
			}
			if event.Categories.Malformed || len(event.Categories.Values) != test.wantCount {
				t.Fatalf("categories = %#v", event.Categories)
			}
		})
	}
}

func TestMalformedCategoriesObjectSkipsOnlyItsEvent(t *testing.T) {
	var page eventsPage
	raw := `{"total":2,"values":[` +
		`{"id":1,"name":"Good","starts_at":"2026-10-01T10:00:00+03:00","location":{"coordinates":["55.75","37.61"]},"categories":{}},` +
		`{"id":2,"name":"Bad","starts_at":"2026-10-01T10:00:00+03:00","location":{"coordinates":["55.75","37.61"]},"categories":{"unexpected":true}}]}`
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Values) != 2 || page.Values[0].Categories.Malformed || !page.Values[1].Categories.Malformed {
		t.Fatalf("decoded categories = %#v, %#v", page.Values[0].Categories, page.Values[1].Categories)
	}
	if _, ok := normalizeEvent(page.Values[0]); !ok {
		t.Fatal("empty categories object must normalize as an empty category list")
	}
	if _, ok := normalizeEvent(page.Values[1]); ok {
		t.Fatal("non-empty categories object must make only that event invalid")
	}
}
