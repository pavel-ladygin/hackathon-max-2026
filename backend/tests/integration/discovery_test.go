package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/discovery"
)

// This test uses unique IDs and removes all rows it creates, so it can run
// against the shared test database without relying on the demo seed.
func TestDiscoverySearchCursorIsExclusiveAndStable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	city, venue, user := uuid.New(), uuid.New(), uuid.New()
	events := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	created := databaseNow(t, db).UTC().Add(24 * time.Hour).Truncate(time.Hour)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", user)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM event_categories WHERE event_id = ANY($1::uuid[])", events)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM events WHERE id = ANY($1::uuid[])", events)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM venues WHERE id = $1", venue)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM cities WHERE id = $1", city)
	})
	if _, err := db.Exec(ctx, `INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,$2,$3,$4,$5)`, city, "Discovery test city", "UTC", 55.75, 37.61); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO venues (id,city_id,name,address,latitude,longitude,venue_type) VALUES ($1,$2,$3,$4,$5,$6,'concert_hall')`, venue, city, "Test venue", "Test address", 55.75, 37.61); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO users (id,max_user_id,display_name,city_id) VALUES ($1,$2,$3,$4)`, user, time.Now().UnixNano(), "Discovery test user", city); err != nil {
		t.Fatal(err)
	}
	for i, eventID := range events {
		starts := created.Add(time.Duration(i) * time.Hour)
		if _, err := db.Exec(ctx, `INSERT INTO events (id,source,external_id,is_demo,title,description,venue_id,starts_at,timezone,price_from_minor,currency,ticket_available,status,published_at) VALUES ($1,'test',$2,false,$3,$4,$5,$6,'UTC',$7,'RUB',true,'published',$6)`, eventID, eventID.String(), "Test event "+eventID.String(), "searchable deterministic event", venue, starts, i*100); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO event_categories (event_id,category_slug,is_primary) VALUES ($1,'concerts',true)`, eventID); err != nil {
			t.Fatal(err)
		}
	}

	repository := discovery.NewRepository(db)
	codec, err := discovery.NewCursorCodec([]byte("discovery-integration-test-key-123456"))
	if err != nil {
		t.Fatal(err)
	}
	service := discovery.NewService(repository, codec)
	filter := discovery.SearchFilter{UserID: user, CityID: city, CategorySlugs: []string{"concerts"}, Limit: 2}
	first, err := service.Search(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Total != 3 || first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}
	if first.Items[0].ID != events[0] || first.Items[1].ID != events[1] {
		t.Fatalf("first order = %v", []uuid.UUID{first.Items[0].ID, first.Items[1].ID})
	}

	filter.Cursor = first.NextCursor
	second, err := service.Search(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != events[2] {
		t.Fatalf("second page = %+v", second)
	}
	if second.Items[0].ID == first.Items[0].ID || second.Items[0].ID == first.Items[1].ID {
		t.Fatalf("cursor returned duplicate: %+v", second.Items)
	}

	repeat, err := service.Search(ctx, discovery.SearchFilter{UserID: user, CityID: city, CategorySlugs: []string{"concerts"}, Limit: 2})
	if err != nil || len(repeat.Items) != 2 || repeat.Items[0].ID != first.Items[0].ID || repeat.Items[1].ID != first.Items[1].ID {
		t.Fatalf("repeat = %+v, err=%v", repeat, err)
	}
}

func TestDiscoverySearchAppliesFreeAndDistanceFilters(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	city, venue, venueWithoutCoordinates, user, event, eventWithoutCoordinates := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE id=$1", user)
		_, _ = db.Exec(ctx, "DELETE FROM event_categories WHERE event_id=ANY($1::uuid[])", []uuid.UUID{event, eventWithoutCoordinates})
		_, _ = db.Exec(ctx, "DELETE FROM events WHERE id=ANY($1::uuid[])", []uuid.UUID{event, eventWithoutCoordinates})
		_, _ = db.Exec(ctx, "DELETE FROM venues WHERE id=ANY($1::uuid[])", []uuid.UUID{venue, venueWithoutCoordinates})
		_, _ = db.Exec(ctx, "DELETE FROM cities WHERE id=$1", city)
	})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,'Filter city','UTC',55.75,37.61)`, []any{city}},
		{`INSERT INTO venues (id,city_id,name,address,latitude,longitude,venue_type) VALUES ($1,$2,'Filter venue','Address',55.75,37.61,'concert_hall')`, []any{venue, city}},
		{`INSERT INTO venues (id,city_id,name,address,latitude,longitude,venue_type) VALUES ($1,$2,'Venue without coordinates','Coordinate-less address',NULL,NULL,'other')`, []any{venueWithoutCoordinates, city}},
		{`INSERT INTO users (id,max_user_id,display_name,city_id) VALUES ($1,$2,'Filter user',$3)`, []any{user, time.Now().UnixNano(), city}},
		{`INSERT INTO events (id,source,external_id,is_demo,title,description,venue_id,starts_at,timezone,price_from_minor,currency,ticket_available,status) VALUES ($1,'test',$2,false,'Free nearby','filter event',$3,$4,'UTC',0,'RUB',true,'published')`, []any{event, event.String(), venue, databaseNow(t, db).UTC().Add(24 * time.Hour)}},
		{`INSERT INTO events (id,source,external_id,is_demo,title,description,venue_id,starts_at,timezone,price_from_minor,currency,ticket_available,status) VALUES ($1,'test',$2,false,'Free without coordinates','filter event',$3,$4,'UTC',0,'RUB',true,'published')`, []any{eventWithoutCoordinates, eventWithoutCoordinates.String(), venueWithoutCoordinates, databaseNow(t, db).UTC().Add(25 * time.Hour)}},
		{`INSERT INTO event_categories (event_id,category_slug,is_primary) VALUES ($1,'concerts',true)`, []any{event}},
		{`INSERT INTO event_categories (event_id,category_slug,is_primary) VALUES ($1,'concerts',true)`, []any{eventWithoutCoordinates}},
	} {
		if _, err := db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	distance := int32(1000)
	codec, err := discovery.NewCursorCodec([]byte("discovery-integration-test-key-123456"))
	if err != nil {
		t.Fatal(err)
	}
	service := discovery.NewService(discovery.NewRepository(db), codec)
	page, err := service.Search(ctx, discovery.SearchFilter{UserID: user, CityID: city, FreeOnly: true, Location: &discovery.Location{Latitude: 55.75, Longitude: 37.61}, DistanceMeters: &distance, Limit: 5})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != event || page.Items[0].PriceFromMinor == nil || *page.Items[0].PriceFromMinor != 0 || page.Items[0].DistanceMeters == nil {
		t.Fatalf("filtered page = %+v, err=%v", page, err)
	}
	withoutDistance, err := service.Search(ctx, discovery.SearchFilter{UserID: user, CityID: city, FreeOnly: true, Location: &discovery.Location{Latitude: 55.75, Longitude: 37.61}, Limit: 5})
	if err != nil || len(withoutDistance.Items) != 2 {
		t.Fatalf("page without distance filter = %+v, err=%v", withoutDistance, err)
	}
	for _, item := range withoutDistance.Items {
		if item.ID == eventWithoutCoordinates && item.DistanceMeters != nil {
			t.Fatalf("coordinate-less event distance = %v, want nil", *item.DistanceMeters)
		}
	}
}
