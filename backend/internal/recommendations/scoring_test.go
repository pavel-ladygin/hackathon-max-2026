package recommendations

import (
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

func TestScoreSnapshotPersistsBehavioralAffinityAndOmitsRemovedFeatures(t *testing.T) {
	eventID := uuid.New()
	event := catalog.Event{Event: platform.Event{ID: eventID}, Categories: []platform.EventCategory{{
		EventID: eventID, CategorySlug: "theatre", IsPrimary: true,
	}}}
	first := normalizedIntent{behavior: normalizedBehavior{categories: map[string]float64{"theatre": .75}}}
	second := normalizedIntent{behavior: normalizedBehavior{categories: map[string]float64{"theatre": -.25}}}
	ranked := scoreEvent(event, platform.Venue{}, first, second)
	if got := ranked.features[featureBehavioralAffinity]; math.Abs(got-.25) > 1e-9 {
		t.Fatalf("behavioral affinity snapshot=%v, want .25", got)
	}
	for _, removed := range []string{"novelty", "popularity", "time_quality"} {
		if _, ok := ranked.features[removed]; ok {
			t.Errorf("removed non-informative feature %q remains in snapshot", removed)
		}
	}
}
