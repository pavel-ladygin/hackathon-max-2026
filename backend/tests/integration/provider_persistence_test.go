package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func TestProviderRepositoryPersistence(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	cityID := uuid.MustParse(catalogseed.MoscowCityID)
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	seedBase := time.Date(2026, 9, 18, 0, 0, 0, 0, moscow)
	if _, err := catalogseed.Apply(ctx, db, seedBase); err != nil {
		t.Fatalf("seed demo catalog: %v", err)
	}
	source := "integration-provider-" + uuid.NewString()
	externalID := "event-" + uuid.NewString()
	venueExternalID := "venue-" + uuid.NewString()
	repo := providers.NewRepository(db)

	event := providerEvent(source, externalID, venueExternalID)
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, `WITH deleted AS (
			DELETE FROM events WHERE source = $1 RETURNING venue_id
		) DELETE FROM venues WHERE id IN (SELECT venue_id FROM deleted)`, source)
		cleanupDemoCatalog(t, db)
	}
	t.Cleanup(cleanup)

	t.Run("first insert and catalog load", func(t *testing.T) {
		result, err := repo.UpsertWithResult(ctx, cityID, event)
		if err != nil {
			t.Fatalf("first upsert: %v", err)
		}
		id := result.EventID
		if id == uuid.Nil {
			t.Fatal("first upsert returned nil event ID")
		}
		if !result.Inserted {
			t.Fatal("first upsert was not reported as inserted")
		}

		var gotSource, gotExternal, gotTitle string
		if err := db.QueryRow(ctx, `SELECT source, external_id, title FROM events WHERE id = $1`, id).Scan(&gotSource, &gotExternal, &gotTitle); err != nil {
			t.Fatalf("read inserted event: %v", err)
		}
		if gotSource != source || gotExternal != externalID || gotTitle != event.Title {
			t.Fatalf("inserted identity/title = %q/%q/%q", gotSource, gotExternal, gotTitle)
		}

		var categories, images int
		if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM event_categories WHERE event_id = $1), (SELECT count(*) FROM event_images WHERE event_id = $1)`, id).Scan(&categories, &images); err != nil {
			t.Fatalf("count dependents: %v", err)
		}
		if categories != 2 || images != 2 {
			t.Fatalf("dependent counts = categories %d, images %d; want 2, 2", categories, images)
		}

		snapshot, err := catalog.NewRepository(db).LoadCity(ctx, cityID)
		if err != nil {
			t.Fatalf("load city: %v", err)
		}
		found := false
		for _, got := range snapshot.Events {
			if got.ID == id {
				found = true
				if got.Title != event.Title || len(got.Categories) != 2 || len(got.Images) != 2 {
					t.Fatalf("loaded provider event = title %q, categories %d, images %d", got.Title, len(got.Categories), len(got.Images))
				}
			}
		}
		if !found {
			t.Fatalf("catalog snapshot does not contain provider event %s", id)
		}
	})

	t.Run("repeat is stable and replacement updates all dependents", func(t *testing.T) {
		first, err := repo.UpsertWithResult(ctx, cityID, event)
		if err != nil {
			t.Fatalf("first upsert: %v", err)
		}
		if first.Inserted {
			t.Fatal("repeat upsert was reported as inserted")
		}
		firstID := first.EventID
		changed := event
		changed.Title = "Changed provider title"
		changed.Description = "Changed provider description"
		changed.Venue.Name = "Changed provider venue"
		changed.Venue.Address = "Changed provider street 2"
		changed.Categories = []providers.NormalizedCategory{{Slug: "sports", Weight: 0.75, IsPrimary: true}}
		changed.Images = []providers.NormalizedImage{{URL: "https://example.test/replaced.jpg", Role: "hero", Position: 0}}
		second, err := repo.UpsertWithResult(ctx, cityID, changed)
		if err != nil {
			t.Fatalf("replacement upsert: %v", err)
		}
		if second.Inserted {
			t.Fatal("replacement upsert was reported as inserted")
		}
		secondID := second.EventID
		if secondID != firstID {
			t.Fatalf("event ID changed from %s to %s", firstID, secondID)
		}

		var title, description, venueName, venueAddress string
		if err := db.QueryRow(ctx, `SELECT e.title, e.description, v.name, v.address FROM events e JOIN venues v ON v.id = e.venue_id WHERE e.id = $1`, firstID).Scan(&title, &description, &venueName, &venueAddress); err != nil {
			t.Fatalf("read replaced event: %v", err)
		}
		if title != changed.Title || description != changed.Description || venueName != changed.Venue.Name || venueAddress != changed.Venue.Address {
			t.Fatalf("updated fields = event %q/%q venue %q/%q", title, description, venueName, venueAddress)
		}
		var categorySlug, imageURL, imageRole string
		if err := db.QueryRow(ctx, `SELECT category_slug FROM event_categories WHERE event_id = $1`, firstID).Scan(&categorySlug); err != nil {
			t.Fatalf("read replacement category: %v", err)
		}
		if err := db.QueryRow(ctx, `SELECT url, role FROM event_images WHERE event_id = $1`, firstID).Scan(&imageURL, &imageRole); err != nil {
			t.Fatalf("read replacement image: %v", err)
		}
		if categorySlug != "sports" || imageURL != changed.Images[0].URL || imageRole != "hero" {
			t.Fatalf("replacement dependents = category %q, image %q/%q", categorySlug, imageURL, imageRole)
		}
		var events, categories, images int
		if err := db.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM events WHERE source = $1 AND external_id = $2),
			(SELECT count(*) FROM event_categories WHERE event_id = $3),
			(SELECT count(*) FROM event_images WHERE event_id = $3)`, source, externalID, firstID).Scan(&events, &categories, &images); err != nil {
			t.Fatalf("count replaced rows: %v", err)
		}
		if events != 1 || categories != 1 || images != 1 {
			t.Fatalf("row counts = events %d categories %d images %d; want 1, 1, 1", events, categories, images)
		}
	})

	t.Run("invalid image role rolls back parent and dependents", func(t *testing.T) {
		bad := event
		bad.ExternalID = "rollback-" + uuid.NewString()
		bad.Venue.ExternalID = "rollback-venue-" + uuid.NewString()
		bad.Venue.Name = "rollback venue " + uuid.NewString()
		bad.Images = []providers.NormalizedImage{{URL: "https://example.test/bad.jpg", Role: "invalid-role", Position: 0}}
		if _, err := repo.Upsert(ctx, cityID, bad); err == nil {
			t.Fatal("invalid image role unexpectedly succeeded")
		}
		var events, venues int
		if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM events WHERE source = $1 AND external_id = $2), (SELECT count(*) FROM venues WHERE name = $3)`, bad.Source, bad.ExternalID, bad.Venue.Name).Scan(&events, &venues); err != nil {
			t.Fatalf("check rollback: %v", err)
		}
		if events != 0 || venues != 0 {
			t.Fatalf("failed upsert left events=%d venues=%d", events, venues)
		}
	})

	t.Run("provider event survives demo reseed", func(t *testing.T) {
		id, err := repo.Upsert(ctx, cityID, event)
		if err != nil {
			t.Fatalf("provider upsert: %v", err)
		}
		if _, err := catalogseed.Apply(ctx, db, seedBase.AddDate(0, 0, 1)); err != nil {
			t.Fatalf("reseed demo catalog: %v", err)
		}
		snapshot, err := catalog.NewRepository(db).LoadCity(ctx, cityID)
		if err != nil {
			t.Fatalf("load reseeded city: %v", err)
		}
		for _, got := range snapshot.Events {
			if got.ID == id {
				return
			}
		}
		t.Fatalf("provider event %s disappeared after demo reseed", id)
	})
}

func providerEvent(source, externalID, venueExternalID string) providers.NormalizedEvent {
	start := time.Date(2026, 10, 4, 19, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	price := int32(1500)
	indoor := true
	age := "12+"
	return providers.NormalizedEvent{
		Source: source, ExternalID: externalID, Title: "Provider integration event",
		Description: "An isolated provider fixture", Venue: providers.NormalizedVenue{
			ExternalID: venueExternalID, Name: "provider venue " + venueExternalID,
			Address: "Provider street 1", Latitude: 55.75, Longitude: 37.61,
			VenueType: "theatre",
		}, StartsAt: start, EndsAt: &end, Timezone: "Europe/Moscow",
		PriceFromMinor: &price, Currency: "RUB", TicketAvailable: true,
		TicketURL: providerStringPtr("https://example.test/tickets"), Status: "published",
		AgeRating: &age, Indoor: &indoor,
		Categories: []providers.NormalizedCategory{{Slug: "theatre", Weight: 1, IsPrimary: true}, {Slug: "walks", Weight: 0.5}},
		Images:     []providers.NormalizedImage{{URL: "https://example.test/card.jpg", Role: "card", Position: 0}, {URL: "https://example.test/gallery.jpg", Role: "gallery", Position: 1}},
	}
}

func providerStringPtr(value string) *string { return &value }
