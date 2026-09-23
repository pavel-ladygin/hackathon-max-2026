package kudago

import (
	"math"
	"testing"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func float64Pointer(value float64) *float64 { return &value }
func int64Pointer(value int64) *int64       { return &value }

var (
	testWindowStart = time.Unix(1_699_999_000, 0).UTC()
	testWindowEnd   = time.Unix(1_700_100_000, 0).UTC()
)

func normalizeTestEvent(event eventDTO) []providers.NormalizedEvent {
	return normalizeEvent(event, testWindowStart, testWindowEnd)
}

func normalizeTestEvents(events []eventDTO) []providers.NormalizedEvent {
	return normalizeEvents(events, testWindowStart, testWindowEnd)
}

func validEvent() eventDTO {
	return eventDTO{
		ID:              42,
		PublicationDate: 1_699_999_000,
		Title:           " Event ",
		ShortTitle:      "Short",
		Tagline:         " Tagline ",
		Description:     "Short description",
		BodyText:        " Full description ",
		Dates:           []eventDate{{Start: 1_700_000_000, End: int64Pointer(1_700_007_200)}},
		Categories:      []string{"concert"},
		AgeRestriction:  "18+",
		Price:           "от 1000 рублей",
		SiteURL:         "https://kudago.com/msk/event/example/",
		Location:        locationDTO{Slug: "msk"},
		Place: &placeDTO{
			ID: 7, Title: " Venue ", Address: " Street 1 ", Subway: " Metro ", Categories: []string{"concert-hall"},
			Coords: coordsDTO{Lat: float64Pointer(55.75), Lon: float64Pointer(37.61)},
		},
	}
}

func TestNormalizePaidEvent(t *testing.T) {
	got := normalizeTestEvent(validEvent())
	if len(got) != 1 {
		t.Fatalf("normalized occurrences=%d, want 1", len(got))
	}
	event := got[0]
	if event.Source != "kudago" || event.ExternalID != "42:1700000000" || event.Title != "Event" || event.Description != "Full description" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.PriceFromMinor != nil || event.PriceToMinor != nil {
		t.Fatalf("text price was parsed: from=%v to=%v", event.PriceFromMinor, event.PriceToMinor)
	}
	if event.TicketURL == nil || *event.TicketURL != "https://kudago.com/msk/event/example/" || !event.TicketAvailable {
		t.Fatalf("unexpected ticket fields: url=%v available=%t", event.TicketURL, event.TicketAvailable)
	}
	if event.Venue.ExternalID != "7" || event.Venue.Name != "Venue" || event.Venue.Address != "Street 1" || event.Venue.Metro == nil || *event.Venue.Metro != "Metro" {
		t.Fatalf("unexpected venue: %+v", event.Venue)
	}
	if event.EndsAt == nil || event.PublishedAt == nil || event.Status != "published" || event.Timezone != "Europe/Moscow" {
		t.Fatalf("unexpected dates/status: %+v", event)
	}
}

func TestNormalizeFreeEvent(t *testing.T) {
	event := validEvent()
	event.IsFree = true
	event.Price = "бесплатно"
	got := normalizeTestEvent(event)[0]
	if got.PriceFromMinor == nil || got.PriceToMinor == nil || *got.PriceFromMinor != 0 || *got.PriceToMinor != 0 {
		t.Fatalf("free price=%v..%v, want 0..0", got.PriceFromMinor, got.PriceToMinor)
	}
}

func TestNormalizeNumericAgeRestriction(t *testing.T) {
	event := validEvent()
	event.AgeRestriction = "18"
	got := normalizeTestEvent(event)
	if len(got) != 1 || got[0].AgeRating == nil || *got[0].AgeRating != "18+" {
		t.Fatalf("normalized age rating = %+v", got)
	}
}

func TestNormalizeUnknownPriceStaysNull(t *testing.T) {
	event := validEvent()
	event.Price = "цена уточняется"
	got := normalizeTestEvent(event)[0]
	if got.PriceFromMinor != nil || got.PriceToMinor != nil {
		t.Fatalf("unknown price was parsed: %+v", got)
	}
}

func TestNormalizeSkipsMissingOrInvalidCoordinates(t *testing.T) {
	for name, mutate := range map[string]func(*eventDTO){
		"missing latitude":       func(event *eventDTO) { event.Place.Coords.Lat = nil },
		"missing longitude":      func(event *eventDTO) { event.Place.Coords.Lon = nil },
		"latitude out of range":  func(event *eventDTO) { event.Place.Coords.Lat = float64Pointer(91) },
		"longitude out of range": func(event *eventDTO) { event.Place.Coords.Lon = float64Pointer(181) },
		"NaN":                    func(event *eventDTO) { event.Place.Coords.Lat = float64Pointer(math.NaN()) },
	} {
		t.Run(name, func(t *testing.T) {
			event := validEvent()
			mutate(&event)
			if got := normalizeTestEvent(event); len(got) != 0 {
				t.Fatalf("normalized invalid coordinates: %+v", got)
			}
		})
	}
}

func TestNormalizeUnknownCategory(t *testing.T) {
	event := validEvent()
	event.Categories = []string{"brand-new-category"}
	got := normalizeTestEvent(event)[0]
	if len(got.Categories) != 1 || got.Categories[0].Slug != "other" || !got.Categories[0].IsPrimary {
		t.Fatalf("categories=%+v", got.Categories)
	}
}

func TestNormalizeProviderPageCTA(t *testing.T) {
	for _, test := range []struct {
		name      string
		raw       string
		available bool
	}{
		{"valid apex", "https://kudago.com/msk/event/example/", true},
		{"valid subdomain", "https://www.kudago.com/msk/event/example/", true},
		{"http", "http://kudago.com/msk/event/example/", false},
		{"wrong host", "https://example.com/event", false},
		{"host suffix attack", "https://kudago.com.attacker.example/event", false},
		{"credentials", "https://user@kudago.com/event", false},
		{"malformed", "://bad", false},
		{"missing", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := validEvent()
			event.SiteURL = test.raw
			got := normalizeTestEvent(event)[0]
			if got.TicketAvailable != test.available || (got.TicketURL != nil) != test.available {
				t.Fatalf("url=%q ticket=%v available=%t", test.raw, got.TicketURL, got.TicketAvailable)
			}
		})
	}
}

func TestNormalizeMultipleAndDuplicateDates(t *testing.T) {
	event := validEvent()
	event.Dates = []eventDate{
		{Start: 1_700_000_000},
		{Start: 1_700_000_000, End: int64Pointer(1_700_001_000)},
		{Start: 1_700_086_400},
	}
	got := normalizeTestEvent(event)
	if len(got) != 2 || got[0].ExternalID != "42:1700000000" || got[1].ExternalID != "42:1700086400" {
		t.Fatalf("occurrences=%+v", got)
	}

	all := normalizeTestEvents([]eventDTO{event, event})
	if len(all) != 2 {
		t.Fatalf("batch deduplication returned %d occurrences", len(all))
	}
}

func TestNormalizeOccurrencesWithinImportWindow(t *testing.T) {
	actualSince := time.Unix(1_700_000_000, 0).UTC()
	actualUntil := time.Unix(1_700_086_400, 0).UTC()
	event := validEvent()
	event.Dates = []eventDate{
		{Start: actualSince.Add(-time.Second).Unix()},
		{Start: actualSince.Unix()},
		{Start: actualSince.Add(12 * time.Hour).Unix()},
		{Start: actualUntil.Unix()},
		{Start: actualUntil.Unix()},
		{Start: actualUntil.Add(time.Second).Unix()},
	}

	got := normalizeEvent(event, actualSince, actualUntil)
	if len(got) != 3 {
		t.Fatalf("normalized occurrences=%d, want 3: %+v", len(got), got)
	}
	wantExternalIDs := []string{"42:1700000000", "42:1700043200", "42:1700086400"}
	for index, want := range wantExternalIDs {
		if got[index].ExternalID != want {
			t.Fatalf("occurrence %d external_id=%q, want %q", index, got[index].ExternalID, want)
		}
	}
}

func TestNormalizeSkipsInvalidStart(t *testing.T) {
	event := validEvent()
	event.Dates = []eventDate{{Start: 0}, {Start: -1}}
	if got := normalizeTestEvent(event); len(got) != 0 {
		t.Fatalf("normalized invalid starts: %+v", got)
	}
}

func TestNormalizeImages(t *testing.T) {
	event := validEvent()
	event.Images = []eventImage{
		{Image: "not-a-url"},
		{Image: "https://cdn.example/card.jpg"},
		{Image: "https://cdn.example/card.jpg"},
		{Image: "http://cdn.example/insecure.jpg"},
		{Image: "https://cdn.example/gallery.jpg"},
	}
	got := normalizeTestEvent(event)[0]
	if len(got.Images) != 2 || got.Images[0].Role != "card" || got.Images[0].Position != 0 || got.Images[1].Role != "gallery" || got.Images[1].Position != 1 {
		t.Fatalf("images=%+v", got.Images)
	}
	if got.Images[0].URL != "https://cdn.example/card.jpg" || got.Images[1].URL != "https://cdn.example/gallery.jpg" {
		t.Fatalf("unexpected image URLs: %+v", got.Images)
	}
}

func TestNormalizeMultipleCategories(t *testing.T) {
	event := validEvent()
	event.Categories = []string{"unknown", "concert", "show", "stand_up", "stand-up"}
	got := normalizeTestEvent(event)[0]
	if len(got.Categories) != 3 {
		t.Fatalf("categories=%+v", got.Categories)
	}
	if got.Categories[0].Slug != "other" || got.Categories[0].IsPrimary || got.Categories[1].Slug != "concerts" || !got.Categories[1].IsPrimary || got.Categories[2].Slug != "standup" || got.Categories[2].IsPrimary {
		t.Fatalf("categories=%+v", got.Categories)
	}
}

func TestNormalizeSkipsMissingRequiredFields(t *testing.T) {
	for name, mutate := range map[string]func(*eventDTO){
		"event ID":   func(event *eventDTO) { event.ID = 0 },
		"title":      func(event *eventDTO) { event.Title = " " },
		"place":      func(event *eventDTO) { event.Place = nil },
		"venue name": func(event *eventDTO) { event.Place.Title = " " },
	} {
		t.Run(name, func(t *testing.T) {
			event := validEvent()
			mutate(&event)
			if got := normalizeTestEvent(event); len(got) != 0 {
				t.Fatalf("normalized event without %s: %+v", name, got)
			}
		})
	}
}

func TestNormalizeAllowsNoImages(t *testing.T) {
	event := validEvent()
	event.Images = nil
	got := normalizeTestEvent(event)[0]
	if len(got.Images) != 0 {
		t.Fatalf("images=%+v", got.Images)
	}
}
