package recommendations

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

func TestCollapseEventSeriesKeepsNearestKudaGoOccurrence(t *testing.T) {
	firstID := uuid.New()
	secondID := uuid.New()
	otherID := uuid.New()

	events := []catalog.Event{
		seriesTestEvent(secondID, "kudago", "184068:1790510400", "2026-09-27T19:00:00Z"),
		seriesTestEvent(otherID, "kudago", "220730:1790424000", "2026-09-26T19:00:00Z"),
		seriesTestEvent(firstID, "kudago", "184068:1790499600", "2026-09-25T19:00:00Z"),
	}

	got := collapseEventSeries(events)

	if len(got) != 2 {
		t.Fatalf("series count=%d, want 2", len(got))
	}

	var found *eventSeries
	for i := range got {
		if eventSeriesKey(got[i].Event) == "kudago:184068" {
			found = &got[i]
			break
		}
	}

	if found == nil {
		t.Fatal("kudago:184068 series not found")
	}
	if found.Event.ID != firstID {
		t.Fatalf("selected event=%s, want nearest %s", found.Event.ID, firstID)
	}
	if found.OtherOccurrences != 1 {
		t.Fatalf("other occurrences=%d, want 1", found.OtherOccurrences)
	}
}

func TestEventSeriesKeyDoesNotCollapseTimepadEvents(t *testing.T) {
	a := seriesTestEvent(uuid.New(), "timepad", "1001", "2026-09-25T19:00:00Z")
	b := seriesTestEvent(uuid.New(), "timepad", "1002", "2026-09-27T19:00:00Z")

	got := collapseEventSeries([]catalog.Event{a, b})

	if len(got) != 2 {
		t.Fatalf("series count=%d, want 2", len(got))
	}
}

func seriesTestEvent(id uuid.UUID, source, externalID, starts string) catalog.Event {
	startsAt, err := time.Parse(time.RFC3339, starts)
	if err != nil {
		panic(err)
	}

	return catalog.Event{
		Event: platform.Event{
			ID:         id,
			Source:     source,
			ExternalID: externalID,
			StartsAt:   pgtype.Timestamptz{Time: startsAt, Valid: true},
		},
	}
}
