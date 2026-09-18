package catalogseed

import (
	"testing"
	"time"
)

func TestEffectiveBaseDateUsesMoscowDate(t *testing.T) {
	moscow, err := time.LoadLocation(timezone)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, value, want string
		now               time.Time
	}{
		{"implicit before Moscow midnight", "", "2026-09-17", time.Date(2026, 9, 17, 20, 59, 0, 0, time.UTC)},
		{"implicit after Moscow midnight", "", "2026-09-18", time.Date(2026, 9, 17, 21, 1, 0, 0, time.UTC)},
		{"explicit date is stable", "2024-02-29", "2024-02-29", time.Date(2030, 1, 1, 12, 0, 0, 0, moscow)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EffectiveBaseDate(tc.value, tc.now)
			if err != nil {
				t.Fatalf("EffectiveBaseDate() error = %v", err)
			}
			if got.Format(time.DateOnly) != tc.want || got.Location().String() != moscow.String() {
				t.Fatalf("date = %s (%s), want %s (%s)", got.Format(time.DateOnly), got.Location(), tc.want, moscow)
			}
		})
	}
	for _, value := range []string{"2026-2-01", "2026-02-30", "yesterday"} {
		t.Run("invalid "+value, func(t *testing.T) {
			if _, err := EffectiveBaseDate(value, time.Now()); err == nil {
				t.Fatal("invalid date unexpectedly accepted")
			}
		})
	}
}

func TestFixturesHaveStableIDsAndDateDependentTimes(t *testing.T) {
	moscow, err := time.LoadLocation(timezone)
	if err != nil {
		t.Fatal(err)
	}
	first := fixtures(time.Date(2026, 9, 18, 0, 0, 0, 0, moscow))
	second := fixtures(time.Date(2027, 1, 5, 0, 0, 0, 0, moscow))
	if len(first.metro) != 12 || len(first.venues) != 12 || len(first.events) != 44 || len(first.images) != 44 {
		t.Fatalf("fixture counts = metro %d, venues %d, events %d, images %d", len(first.metro), len(first.venues), len(first.events), len(first.images))
	}
	if len(first.categories) != 55 || len(categorySlugs) != 11 {
		t.Fatalf("categories = %d rows and %d distinct slugs, want 55 and 11", len(first.categories), len(categorySlugs))
	}
	if len(first.events) != len(second.events) {
		t.Fatal("fixture event count changed")
	}
	for i := range first.events {
		if first.events[i].ID != second.events[i].ID || first.events[i].ExternalID != second.events[i].ExternalID {
			t.Fatalf("event %d identity changed: %s/%s vs %s/%s", i, first.events[i].ID, first.events[i].ExternalID, second.events[i].ID, second.events[i].ExternalID)
		}
		if !first.events[i].StartsAt.Time.Before(second.events[i].StartsAt.Time) {
			t.Fatalf("event %d start did not shift with base date", i)
		}
	}
	for i := range first.metro {
		if first.metro[i].ID != second.metro[i].ID {
			t.Fatalf("metro %d identity changed", i)
		}
	}
	for i := range first.venues {
		if first.venues[i].ID != second.venues[i].ID {
			t.Fatalf("venue %d identity changed", i)
		}
	}
}
