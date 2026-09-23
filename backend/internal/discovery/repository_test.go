package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDiscoveryQueriesExcludeDemoInactiveAndStartedEvents(t *testing.T) {
	queryPath := filepath.Join("..", "store", "platform", "queries", "discovery.sql")
	query, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("read discovery query contract: %v", err)
	}

	for _, name := range []string{
		"SearchDiscoveryEventCards",
		"CountDiscoveryEventCards",
		"GetDiscoveryEventDetail",
	} {
		t.Run(name, func(t *testing.T) {
			section := discoveryQuerySection(t, string(query), name)
			for _, predicate := range []string{"e.is_demo = false", "e.provider_active = true", "e.starts_at > now()"} {
				if !strings.Contains(section, predicate) {
					t.Errorf("%s must contain %q to hide demo and already-started events", name, predicate)
				}
			}
		})
	}
}

func discoveryQuerySection(t *testing.T, query, name string) string {
	t.Helper()
	marker := "-- name: " + name
	start := strings.Index(query, marker)
	if start < 0 {
		t.Fatalf("query %q not found", name)
	}
	section := query[start+len(marker):]
	if next := strings.Index(section, "-- name:"); next >= 0 {
		section = section[:next]
	}
	return section
}

func TestSearchArgumentsPreserveUserFilterAndExclusiveCursor(t *testing.T) {
	user, city, eventID := uuid.New(), uuid.New(), uuid.New()
	startsAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	price := int32(0)
	distance := int32(500)
	filter := SearchFilter{
		UserID: user, CityID: city, Query: stringPtr("music"),
		CategorySlugs: []string{"concerts"}, PriceMaxMinor: &price, FreeOnly: true,
		Location: &Location{Latitude: 55.75, Longitude: 37.61}, DistanceMeters: &distance,
		Cursor: &Cursor{StartsAt: startsAt, EventID: eventID},
	}
	params := searchParams(filter, 21)
	if params.UserID != user || params.CityID != city || !params.Query.Valid || params.Query.String != "music" ||
		len(params.CategorySlugs) != 1 || params.CategorySlugs[0] != "concerts" || !params.PriceMaxMinor.Valid || params.PriceMaxMinor.Int32 != 0 ||
		!params.FreeOnly || !params.DistanceMeters.Valid || params.DistanceMeters.Int32 != 500 || params.LimitCount != 21 {
		t.Fatalf("search params lost bindings: %#v", params)
	}
	if !params.CursorStartsAt.Valid || !params.CursorStartsAt.Time.Equal(startsAt) || !params.CursorEventID.Valid || uuid.UUID(params.CursorEventID.Bytes) != eventID {
		t.Fatalf("cursor params = %#v / %#v", params.CursorStartsAt, params.CursorEventID)
	}
}

func TestLocationArgumentsDoNotPersistCoordinatesWhenLocationAbsent(t *testing.T) {
	latitude, longitude := locationValues(nil)
	if latitude.Valid || longitude.Valid {
		t.Fatalf("nil location arguments = (%v, %v), want nil pair", latitude, longitude)
	}
	location := &Location{Latitude: 55.75, Longitude: 37.61}
	latitude, longitude = locationValues(location)
	if !latitude.Valid || !longitude.Valid || latitude.Float64 != location.Latitude || longitude.Float64 != location.Longitude {
		t.Fatalf("location arguments = (%v, %v)", latitude, longitude)
	}
}
