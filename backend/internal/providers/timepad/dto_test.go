package timepad

import (
	"encoding/json"
	"fmt"
	"testing"
)

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
