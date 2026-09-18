package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
)

func TestCheckForRoomVoteReadsCurrentAvailability(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	cityID, venueID, eventID := uuid.New(), uuid.New(), uuid.New()
	_, err := db.Exec(ctx, "INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,'availability-test-city','UTC',0,0)", cityID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, "INSERT INTO venues (id,city_id,name,address,latitude,longitude) VALUES ($1,$2,'availability-test-venue','test',0,0)", venueID, cityID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO events (id,source,external_id,title,description,venue_id,starts_at,timezone,price_from_minor,price_to_minor,currency,ticket_url,ticket_available,status)
		VALUES ($1,'test',$2,'availability test','description',$3,$4,'UTC',$5,$6,'RUB',$7,$8,$9)`, eventID, eventID.String(), venueID, time.Now().UTC(), 100, 250, "https://tickets.example/initial", true, "published")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, "DELETE FROM events WHERE id=$1", eventID)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM venues WHERE id=$1", venueID)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM cities WHERE id=$1", cityID)
	})

	repo := catalog.NewRepository(db)
	got, err := repo.CheckForRoomVote(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	assertAvailability(t, got, true, "published", true, int32Ptr(100), int32Ptr(250), stringPtr("https://tickets.example/initial"))

	if _, err := db.Exec(ctx, "UPDATE events SET status='sold_out', ticket_available=false, price_from_minor=NULL, price_to_minor=NULL, ticket_url=NULL WHERE id=$1", eventID); err != nil {
		t.Fatal(err)
	}
	got, err = repo.CheckForRoomVote(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	assertAvailability(t, got, true, "sold_out", false, nil, nil, nil)

	if _, err := db.Exec(ctx, "UPDATE events SET status='cancelled', ticket_available=true, price_from_minor=0, price_to_minor=0, ticket_url='https://tickets.example/updated' WHERE id=$1", eventID); err != nil {
		t.Fatal(err)
	}
	got, err = repo.CheckForRoomVote(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	assertAvailability(t, got, true, "cancelled", true, int32Ptr(0), int32Ptr(0), stringPtr("https://tickets.example/updated"))
}

func TestCheckForRoomVoteUnknownIDAndContextError(t *testing.T) {
	db := openTestDB(t)
	repo := catalog.NewRepository(db)
	got, err := repo.CheckForRoomVote(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unknown event error = %v", err)
	}
	if got.Exists {
		t.Fatalf("unknown event availability = %+v, want Exists=false", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = repo.CheckForRoomVote(ctx, uuid.New())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v, want context.Canceled", err)
	}
}

func assertAvailability(t *testing.T, got contracts.Availability, exists bool, status string, ticketAvailable bool, from, to *int32, url *string) {
	t.Helper()
	if got.Exists != exists || got.Status != status || got.Currency != "RUB" || got.TicketAvailable != ticketAvailable {
		t.Fatalf("availability flags = %+v", got)
	}
	if !optionalInt32Equal(got.PriceFromMinor, from) || !optionalInt32Equal(got.PriceToMinor, to) {
		t.Fatalf("prices = from %v to %v, want %v/%v", got.PriceFromMinor, got.PriceToMinor, from, to)
	}
	if !optionalStringEqual(got.TicketURL, url) {
		t.Fatalf("ticket URL = %v, want %v", got.TicketURL, url)
	}
}

func int32Ptr(value int32) *int32    { return &value }
func stringPtr(value string) *string { return &value }

func optionalInt32Equal(left, right *int32) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func optionalStringEqual(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}
