package generic

import "testing"

// These cases extend the existing Normalize fixture. They were recorded failing
// on the G1 baseline before changing ingestion; no provider or database is used.
func TestNormalizeRejectsContradictoryQualityFields(t *testing.T) {
	for _, kind := range []string{"end_before_start", "end_equals_start", "inverted_price", "latitude_outside_range", "longitude_outside_range"} {
		t.Run(kind, func(t *testing.T) {
			cfg := testConfig()
			cfg.Fields.EndsAt = FieldMapping{Path: "end", Transform: TransformISODateTime}
			cfg.Fields.Latitude = FieldMapping{Path: "lat", Transform: TransformNumber}
			cfg.Fields.Longitude = FieldMapping{Path: "lng", Transform: TransformNumber}
			cfg.PriceFrom = FieldMapping{Path: "min", Transform: TransformNumber}
			cfg.PriceTo = FieldMapping{Path: "max", Transform: TransformNumber}
			record := map[string]any{
				"id": "quality-fixture", "event": map[string]any{"title": "Fixture"},
				"schedule": map[string]any{"start": "2026-10-24T19:00:00+03:00"},
				"place":    map[string]any{"name": "Fixture hall"},
				"end":      "2026-10-24T21:00:00+03:00", "lat": 55.75, "lng": 37.61, "min": 1000, "max": 2000,
			}
			switch kind {
			case "end_before_start":
				record["end"] = "2026-10-24T18:00:00+03:00"
			case "end_equals_start":
				record["end"] = "2026-10-24T19:00:00+03:00"
			case "inverted_price":
				record["max"] = 500
			case "latitude_outside_range":
				record["lat"] = 91
			case "longitude_outside_range":
				record["lng"] = -181
			}
			if _, err := Normalize(cfg, record); err == nil {
				t.Fatal("contradictory fixture record accepted")
			}
		})
	}
}

func TestQualityValidationPreservesUnknownAndExplicitFree(t *testing.T) {
	cfg := testConfig()
	cfg.PriceFrom = FieldMapping{Path: "min", Transform: TransformNumber}
	cfg.PriceTo = FieldMapping{Path: "max", Transform: TransformNumber}
	record := map[string]any{
		"id": "quality-control", "event": map[string]any{"title": "Fixture"},
		"schedule": map[string]any{"start": "2026-10-24T19:00:00+03:00"},
		"place":    map[string]any{"name": "Fixture hall"},
	}
	unknown, err := Normalize(cfg, record)
	if err != nil || unknown.PriceFromMinor != nil || unknown.PriceToMinor != nil || unknown.EndsAt != nil {
		t.Fatal("missing price or end was invented or rejected")
	}
	record["min"], record["max"] = 0, 0
	free, err := Normalize(cfg, record)
	if err != nil || free.PriceFromMinor == nil || *free.PriceFromMinor != 0 || free.PriceToMinor == nil || *free.PriceToMinor != 0 {
		t.Fatal("explicit zero price was lost")
	}
}
