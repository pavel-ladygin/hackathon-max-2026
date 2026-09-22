package integration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
)

func TestDemoCatalogSeedAndRepository(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	cityID := uuid.MustParse(catalogseed.MoscowCityID)
	t.Cleanup(func() { cleanupDemoCatalog(t, db) })

	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 18, 0, 0, 0, 0, moscow)
	wantCounts := catalogseed.Counts{Cities: 1, MetroStations: 12, Venues: 12, Categories: 11, Events: 44, Images: 44}
	if got, err := catalogseed.Apply(ctx, db, base); err != nil {
		t.Fatalf("first seed: %v", err)
	} else if got != wantCounts {
		t.Fatalf("seed counts = %+v, want %+v", got, wantCounts)
	}
	repo := catalog.NewRepository(db)
	first, err := repo.LoadCity(ctx, cityID)
	if err != nil {
		t.Fatalf("load seeded city: %v", err)
	}
	first = demoCatalogSnapshot(first)
	assertCatalogSnapshot(t, first, base)

	if got, err := catalogseed.Apply(ctx, db, base); err != nil {
		t.Fatalf("repeat seed: %v", err)
	} else if got != wantCounts {
		t.Fatalf("repeat seed counts = %+v, want %+v", got, wantCounts)
	}
	repeated, err := repo.LoadCity(ctx, cityID)
	if err != nil {
		t.Fatalf("load repeated seed: %v", err)
	}
	repeated = demoCatalogSnapshot(repeated)
	if !reflect.DeepEqual(first, repeated) {
		t.Fatal("seeding the same base date changed the catalog snapshot")
	}

	shiftedBase := base.AddDate(0, 0, 10)
	if _, err := catalogseed.Apply(ctx, db, shiftedBase); err != nil {
		t.Fatalf("shifted seed: %v", err)
	}
	shifted, err := repo.LoadCity(ctx, cityID)
	if err != nil {
		t.Fatalf("load shifted seed: %v", err)
	}
	shifted = demoCatalogSnapshot(shifted)
	assertCatalogSnapshot(t, shifted, shiftedBase)
	if len(first.Events) != len(shifted.Events) {
		t.Fatalf("shifted event count = %d, want %d", len(shifted.Events), len(first.Events))
	}
	for i := range first.Events {
		if first.Events[i].ID != shifted.Events[i].ID {
			t.Fatalf("event %d ID changed after base shift", i)
		}
		if !shifted.Events[i].StartsAt.Time.After(first.Events[i].StartsAt.Time) {
			t.Fatalf("event %s start did not move after base shift", first.Events[i].ID)
		}
	}

	// A provider record in the same city must survive seed reconciliation.
	liveID := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO events
		(id, source, external_id, title, description, venue_id, starts_at, timezone, status)
		SELECT $1, 'provider', 'catalog-preservation-test', title, description, venue_id, starts_at, timezone, status
		FROM events WHERE id = $2`, liveID, shifted.Events[0].ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), "DELETE FROM events WHERE id = $1", liveID); err != nil {
			t.Error(err)
		}
	})
	withLive, err := repo.LoadCity(ctx, cityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalogseed.Apply(ctx, db, shiftedBase); err != nil {
		t.Fatal(err)
	}
	afterLive, err := repo.LoadCity(ctx, cityID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withLive, afterLive) {
		t.Fatal("seed changed an unrelated provider record")
	}

	// A collision encountered late in the batch must roll back earlier writes,
	// including category/image replacement and all shifted timestamps.
	conflictID := shifted.Events[len(shifted.Events)-1].ID
	if _, err := db.Exec(ctx, "UPDATE events SET source = 'provider', is_demo = false WHERE id = $1", conflictID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), "UPDATE events SET source = 'demo', is_demo = true WHERE id = $1", conflictID); err != nil {
			t.Error(err)
		}
	})
	beforeConflict, err := repo.LoadCity(ctx, cityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalogseed.Apply(ctx, db, shiftedBase.AddDate(0, 0, 1)); err == nil {
		t.Fatal("non-demo identity collision was overwritten")
	}
	afterConflict, err := repo.LoadCity(ctx, cityID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeConflict, afterConflict) {
		t.Fatal("failed seed did not roll back the complete catalog transaction")
	}

	if _, err := repo.LoadCity(ctx, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing city error = %v, want pgx.ErrNoRows", err)
	}
}

func demoCatalogSnapshot(snapshot catalog.Snapshot) catalog.Snapshot {
	demoVenues := make(map[uuid.UUID]bool)
	events := snapshot.Events[:0]
	for _, event := range snapshot.Events {
		if event.Source == "demo" && event.IsDemo {
			events = append(events, event)
			demoVenues[event.VenueID] = true
		}
	}
	venues := snapshot.Venues[:0]
	for _, venue := range snapshot.Venues {
		if demoVenues[venue.ID] {
			venues = append(venues, venue)
		}
	}
	snapshot.Events = events
	snapshot.Venues = venues
	return snapshot
}

func assertCatalogSnapshot(t *testing.T, snapshot catalog.Snapshot, base time.Time) {
	t.Helper()
	if snapshot.City.ID != uuid.MustParse(catalogseed.MoscowCityID) || snapshot.City.Name != "Москва" {
		t.Fatalf("unexpected city: %+v", snapshot.City)
	}
	if len(snapshot.MetroStations) != 12 || len(snapshot.Venues) != 12 || len(snapshot.Events) != 44 {
		t.Fatalf("snapshot counts = metro %d, venues %d, events %d; want 12, 12, 44", len(snapshot.MetroStations), len(snapshot.Venues), len(snapshot.Events))
	}
	statuses := map[string]int{}
	loudness := map[string]int{}
	prices := map[string]int{}
	ticketStates := map[string]int{}
	categorySlugs := map[string]int{}
	for _, event := range snapshot.Events {
		if event.Source != "demo" || !event.IsDemo || event.Timezone != "Europe/Moscow" {
			t.Fatalf("event %s has unexpected provenance/timezone: source=%q demo=%v timezone=%q", event.ID, event.Source, event.IsDemo, event.Timezone)
		}
		if !event.SourceUpdatedAt.Time.Equal(base) || !event.PublishedAt.Time.Equal(base) || !event.UpdatedAt.Time.Equal(base) {
			t.Fatalf("event %s timestamps do not use base date", event.ID)
		}
		if !event.StartsAt.Time.After(base.AddDate(0, 0, 1)) || !event.EndsAt.Time.After(event.StartsAt.Time) {
			t.Fatalf("event %s must start after the base day and end after its start", event.ID)
		}
		statuses[event.Status]++
		if event.LoudnessLevel.Valid {
			loudness[event.LoudnessLevel.String]++
		}
		switch {
		case !event.PriceFromMinor.Valid:
			prices["null"]++
		case event.PriceFromMinor.Int32 == 0:
			prices["zero"]++
		default:
			prices["positive"]++
		}
		if event.TicketAvailable && event.TicketUrl.Valid {
			ticketStates["published-url"]++
		} else if !event.TicketAvailable && !event.TicketUrl.Valid {
			ticketStates["unavailable-null"]++
		} else if !event.TicketAvailable && event.TicketUrl.Valid {
			ticketStates["unavailable-url"]++
		} else {
			t.Fatalf("event %s has invalid ticket state: available=%v url=%v", event.ID, event.TicketAvailable, event.TicketUrl.Valid)
		}
		primary := 0
		for _, category := range event.Categories {
			categorySlugs[category.CategorySlug]++
			if category.IsPrimary {
				primary++
			}
		}
		if primary != 1 || len(event.Images) != 1 {
			t.Fatalf("event %s has %d primary categories and %d images; want 1 and 1", event.ID, primary, len(event.Images))
		}
	}
	if len(categorySlugs) != 11 {
		t.Fatalf("distinct category slugs = %d, want 11", len(categorySlugs))
	}
	for _, slug := range []string{"concerts", "cinema", "theatre", "standup", "exhibitions", "sports", "food", "parties", "festivals", "walks", "other"} {
		if categorySlugs[slug] == 0 {
			t.Errorf("canonical category %q is absent", slug)
		}
	}
	for key, want := range map[string]int{"published": 42, "sold_out": 1, "cancelled": 1} {
		if statuses[key] != want {
			t.Fatalf("status %q count = %d, want %d", key, statuses[key], want)
		}
	}
	for key := range map[string]bool{"quiet": true, "normal": true, "loud": true, "very_loud": true} {
		if loudness[key] != 11 {
			t.Fatalf("loudness %q count = %d, want 11", key, loudness[key])
		}
	}
	for key, want := range map[string]int{"zero": 11, "null": 11, "positive": 22} {
		if prices[key] != want {
			t.Fatalf("price %q count = %d, want %d", key, prices[key], want)
		}
	}
	if ticketStates["published-url"] != 31 || ticketStates["unavailable-null"] != 11 || ticketStates["unavailable-url"] != 2 {
		t.Fatalf("ticket states = %+v, want 31 published URLs, 11 unavailable nulls, and 2 unavailable URLs", ticketStates)
	}
}

func cleanupDemoCatalog(t *testing.T, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}) {
	t.Helper()
	// Provider events may share Moscow with the demo catalog. Remove only demo
	// events and rows that become orphaned as a result.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	queries := []string{
		"DELETE FROM events WHERE source = 'demo' AND is_demo AND venue_id IN (SELECT id FROM venues WHERE city_id = $1)",
		"DELETE FROM venues v WHERE city_id = $1 AND NOT EXISTS (SELECT 1 FROM events e WHERE e.venue_id=v.id)",
		"DELETE FROM metro_stations m WHERE city_id = $1 AND NOT EXISTS (SELECT 1 FROM events e JOIN venues v ON v.id=e.venue_id WHERE v.city_id=m.city_id)",
		"DELETE FROM cities c WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM venues v WHERE v.city_id=c.id) AND NOT EXISTS (SELECT 1 FROM users u WHERE u.city_id=c.id) AND NOT EXISTS (SELECT 1 FROM rooms r WHERE r.city_id=c.id)",
	}
	for _, query := range queries {
		if _, err := db.Exec(ctx, query, catalogseed.MoscowCityID); err != nil {
			t.Logf("demo cleanup query failed: %v", err)
			return
		}
	}
}
