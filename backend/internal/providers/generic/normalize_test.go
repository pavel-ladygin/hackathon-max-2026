package generic

import (
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		SourceKey: "generic:community-events",
		Fields: Fields{
			ExternalID: FieldMapping{Path: "id", Transform: TransformString},
			Title:      FieldMapping{Path: "event.title", Transform: TransformStripHTML},
			StartsAt:   FieldMapping{Path: "schedule.start", Transform: TransformISODateTime},
			VenueName:  FieldMapping{Path: "place.name", Transform: TransformString},
		},
		Defaults:  Defaults{Category: "concerts", Timezone: "Europe/Moscow", Currency: "RUB", Status: "published"},
		PriceUnit: PriceUnitMajor,
	}
}

func TestNormalizeMapsNestedFieldsAndArrayIndex(t *testing.T) {
	config := testConfig()
	config.Fields.Description = FieldMapping{Path: "details.description", Transform: TransformStripHTML}
	config.Fields.Image = FieldMapping{Path: "images.0.url", Transform: TransformString}
	config.Fields.TicketURL = FieldMapping{Path: "links.ticket", Transform: TransformString}
	record := map[string]any{
		"id":       42,
		"event":    map[string]any{"title": "  <b>Evening show</b>  "},
		"schedule": map[string]any{"start": "2026-10-03T19:00:00+03:00"},
		"place":    map[string]any{"name": "Club"},
		"details":  map[string]any{"description": "<p>Live <b>music</b> &amp; more</p>"},
		"images":   []any{map[string]any{"url": "https://example.test/poster.jpg"}},
		"links":    map[string]any{"ticket": "https://example.test/tickets"},
	}

	event, err := Normalize(config, record)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if event.Source != "generic:community-events" || event.ExternalID != "42" || event.Title != "Evening show" {
		t.Fatalf("identity/title = (%q, %q, %q)", event.Source, event.ExternalID, event.Title)
	}
	if event.Description != "Live music & more" {
		t.Errorf("Description = %q", event.Description)
	}
	if len(event.Images) != 1 || event.Images[0].URL != "https://example.test/poster.jpg" {
		t.Errorf("Images = %#v", event.Images)
	}
	if event.Images[0].Role != "card" {
		t.Errorf("image role = %q, want card", event.Images[0].Role)
	}
	if event.TicketURL == nil || *event.TicketURL != "https://example.test/tickets" {
		t.Errorf("TicketURL = %v", event.TicketURL)
	}
	_, offset := event.StartsAt.Zone()
	if offset != 3*60*60 || event.StartsAt.Hour() != 19 {
		t.Errorf("StartsAt lost explicit offset: %s", event.StartsAt)
	}
	if event.Venue.Name != "Club" || event.Timezone != "Europe/Moscow" || event.Currency != "RUB" || event.Status != "published" {
		t.Errorf("required canonical values missing: %#v", event)
	}
	if len(event.Categories) != 1 || event.Categories[0].Slug != "concerts" || !event.Categories[0].IsPrimary {
		t.Errorf("Categories = %#v", event.Categories)
	}
}

func TestNormalizeAppliesDefaultsAndConvertsPrices(t *testing.T) {
	config := testConfig()
	config.Fields.Description = FieldMapping{Path: "missing.description", Default: stringPtr("Описание по умолчанию")}
	config.Fields.Subtitle = FieldMapping{Path: "missing.subtitle", Default: stringPtr("Вечер")}
	config.Fields.StartsAt = FieldMapping{Path: "timestamp", Transform: TransformUnixTimestamp}
	config.PriceFrom = FieldMapping{Path: "price.from", Transform: TransformNumber}
	config.PriceTo = FieldMapping{Path: "price.to", Transform: TransformNumber}
	record := map[string]any{
		"id": "evt-1", "event": map[string]any{"title": "Show"}, "timestamp": float64(1791043200),
		"place": map[string]any{"name": "Hall"}, "price": map[string]any{"from": "12.50", "to": 25},
	}

	event, err := Normalize(config, record)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if event.Description != "Описание по умолчанию" || event.Subtitle == nil || *event.Subtitle != "Вечер" {
		t.Errorf("defaults not applied: description=%q subtitle=%v", event.Description, event.Subtitle)
	}
	if event.StartsAt.Unix() != 1791043200 {
		t.Errorf("Unix timestamp = %d, want 1791043200", event.StartsAt.Unix())
	}
	if event.PriceFromMinor == nil || *event.PriceFromMinor != 1250 || event.PriceToMinor == nil || *event.PriceToMinor != 2500 {
		t.Errorf("major-unit prices not converted to minor units: from=%v to=%v", event.PriceFromMinor, event.PriceToMinor)
	}
}

func TestNormalizeMinorUnitPricesAndOptionalImage(t *testing.T) {
	config := testConfig()
	config.PriceUnit = PriceUnitMinor
	config.PriceFrom = FieldMapping{Path: "price", Transform: TransformNumber}
	event, err := Normalize(config, map[string]any{
		"id": "evt-2", "event": map[string]any{"title": "Show"}, "schedule": map[string]any{"start": "2026-10-03T19:00:00"},
		"place": map[string]any{"name": "Hall"}, "price": 990,
	})
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if event.PriceFromMinor == nil || *event.PriceFromMinor != 990 {
		t.Errorf("minor-unit price = %v, want 990", event.PriceFromMinor)
	}
	if len(event.Images) != 0 {
		t.Errorf("missing optional image produced images: %#v", event.Images)
	}
	// A timestamp without an explicit offset is interpreted in the configured zone.
	_, offset := event.StartsAt.Zone()
	if offset != 3*60*60 {
		t.Errorf("naive ISO datetime zone offset = %d, want Moscow +03:00", offset)
	}
}

func TestNormalizeRejectsMissingRequiredValues(t *testing.T) {
	base := map[string]any{
		"id": "evt", "event": map[string]any{"title": "Show"},
		"schedule": map[string]any{"start": "2026-10-03T19:00:00+03:00"},
		"place":    map[string]any{"name": "Hall"},
	}
	tests := []struct {
		name   string
		config Config
		record map[string]any
	}{
		{name: "external id", config: func() Config { c := testConfig(); c.Fields.ExternalID.Path = "absent"; return c }(), record: base},
		{name: "title", config: func() Config { c := testConfig(); c.Fields.Title.Path = "absent"; return c }(), record: base},
		{name: "start", config: func() Config { c := testConfig(); c.Fields.StartsAt.Path = "absent"; return c }(), record: base},
		{name: "venue name", config: func() Config { c := testConfig(); c.Fields.VenueName.Path = "absent"; return c }(), record: base},
		{name: "category default", config: func() Config { c := testConfig(); c.Defaults.Category = ""; return c }(), record: base},
		{name: "timezone default", config: func() Config { c := testConfig(); c.Defaults.Timezone = ""; return c }(), record: base},
		{name: "currency default", config: func() Config { c := testConfig(); c.Defaults.Currency = ""; return c }(), record: base},
		{name: "status default", config: func() Config { c := testConfig(); c.Defaults.Status = ""; return c }(), record: base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Normalize(tt.config, tt.record); err == nil {
				t.Fatal("Normalize() error = nil, want required-value error")
			}
		})
	}
}

func TestNormalizeRejectsInvalidPathAndTypedValue(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		record map[string]any
	}{
		{name: "array index out of range", config: func() Config { c := testConfig(); c.Fields.VenueName.Path = "places.3.name"; return c }(), record: map[string]any{"id": "evt", "event": map[string]any{"title": "Show"}, "schedule": map[string]any{"start": "2026-10-03T19:00:00Z"}, "places": []any{map[string]any{"name": "Hall"}}}},
		{name: "invalid number", config: func() Config {
			c := testConfig()
			c.PriceFrom = FieldMapping{Path: "price", Transform: TransformNumber}
			return c
		}(), record: map[string]any{"id": "evt", "event": map[string]any{"title": "Show"}, "schedule": map[string]any{"start": "2026-10-03T19:00:00Z"}, "place": map[string]any{"name": "Hall"}, "price": "free-ish"}},
		{name: "invalid date", config: func() Config { c := testConfig(); return c }(), record: map[string]any{"id": "evt", "event": map[string]any{"title": "Show"}, "schedule": map[string]any{"start": "next Saturday"}, "place": map[string]any{"name": "Hall"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Normalize(tt.config, tt.record); err == nil {
				t.Fatal("Normalize() error = nil, want mapping error")
			} else if strings.TrimSpace(err.Error()) == "" {
				t.Fatal("Normalize() returned an empty error")
			}
		})
	}
}

func TestNormalizeISODateWithoutOffsetUsesConfiguredLocation(t *testing.T) {
	event, err := Normalize(testConfig(), map[string]any{
		"id": "evt", "event": map[string]any{"title": "Show"},
		"schedule": map[string]any{"start": "2026-10-03T19:00:00"}, "place": map[string]any{"name": "Hall"},
	})
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 3, 19, 0, 0, 0, location)
	if !event.StartsAt.Equal(want) || event.StartsAt.Location().String() != location.String() {
		t.Errorf("StartsAt = %s (%s), want %s (%s)", event.StartsAt, event.StartsAt.Location(), want, location)
	}
}

func TestValidateMappingsRejectsUnsupportedTransformsAndMalformedPaths(t *testing.T) {
	tests := []struct {
		name    string
		fields  Fields
		from    FieldMapping
		to      FieldMapping
		wantErr bool
	}{
		{name: "supported mappings", fields: Fields{Title: FieldMapping{Path: "event.title", Transform: TransformStripHTML}, StartsAt: FieldMapping{Path: "event.start", Transform: TransformISODateTime}}, from: FieldMapping{Path: "price", Transform: TransformNumber}},
		{name: "string transform on date", fields: Fields{StartsAt: FieldMapping{Path: "start", Transform: TransformString}}, wantErr: true},
		{name: "unknown transform", fields: Fields{Title: FieldMapping{Path: "title", Transform: Transform("expression")}}, wantErr: true},
		{name: "empty path segment", fields: Fields{Title: FieldMapping{Path: "event..title"}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateMappings(test.fields, test.from, test.to)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateMappings error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestNormalizeRejectsOutOfRangePricesAndCoordinates(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		extra  map[string]any
	}{
		{name: "negative price", config: func() Config {
			c := testConfig()
			c.PriceFrom = FieldMapping{Path: "price", Transform: TransformNumber}
			return c
		}(), extra: map[string]any{"price": -1}},
		{name: "latitude above range", config: func() Config {
			c := testConfig()
			c.Fields.Latitude = FieldMapping{Path: "latitude", Transform: TransformNumber}
			return c
		}(), extra: map[string]any{"latitude": 91}},
		{name: "longitude below range", config: func() Config {
			c := testConfig()
			c.Fields.Longitude = FieldMapping{Path: "longitude", Transform: TransformNumber}
			return c
		}(), extra: map[string]any{"longitude": -181}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := map[string]any{
				"id": "evt", "event": map[string]any{"title": "Show"},
				"schedule": map[string]any{"start": "2026-10-03T19:00:00Z"}, "place": map[string]any{"name": "Hall"},
			}
			for key, value := range test.extra {
				record[key] = value
			}
			if _, err := Normalize(test.config, record); err == nil {
				t.Fatal("Normalize() accepted an out-of-range value")
			}
		})
	}
}

func stringPtr(value string) *string { return &value }
