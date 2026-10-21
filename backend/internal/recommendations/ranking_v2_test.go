package recommendations

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

func TestCategoryDiscoveryRelevanceBand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		score   float64
		promote bool
	}{
		{"inside", .76, true},
		{"inclusive boundary", .75, true},
		{"outside boundary", math.Nextafter(.75, 0), false},
		{"unrelated", .1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ranked := []rankedEvent{
				{primary: "music", groupScore: .8},
				{primary: "music", groupScore: .8},
				{primary: "theatre", groupScore: tc.score},
			}
			// Only discovery differs: no caps bind at the second position.
			got := chooseDiverse(ranked, []bool{true, false, false}, ranked[:1], map[string]bool{"music": true}, true, true, true)
			if (got == 2) != tc.promote || (!tc.promote && got != -1) {
				t.Fatalf("discovery index=%d, promote=%t", got, tc.promote)
			}
		})
	}
}

func TestRelevanceBandKeepsCapsAndBoundsEachDiscovery(t *testing.T) {
	var ranked []rankedEvent
	for i := 1; i <= 8; i++ {
		ranked = append(ranked, rankedEvent{
			event: catalog.Event{Event: platform.Event{ID: evaluationID("event", i)}},
			venue: platform.Venue{ID: evaluationID("venue", (i-1)%3+1)}, primary: "music", groupScore: .8,
		})
	}
	ranked = append(ranked, rankedEvent{
		event: catalog.Event{Event: platform.Event{ID: evaluationID("event", 9)}},
		venue: platform.Venue{ID: evaluationID("venue", 4)}, primary: "cinema", groupScore: .1,
	})
	got := diversify(ranked)
	if len(got) != len(ranked) || got[1].primary != "music" || got[2].primary != "music" || got[3].primary != "cinema" {
		t.Fatal("weak category must wait until required by the category cap")
	}
	// The cap can require a drop greater than the discovery band: it is a
	// separate presentation constraint and is intentionally preserved.
	if respectsCaps(ranked[3], got[:3], true, true) {
		t.Fatal("fourth consecutive music event unexpectedly satisfies the cap")
	}
	// Every heuristic discovery is bounded relative to the best candidate that
	// obeys exactly the same caps, including after prior picks deplete the band.
	used := make([]bool, len(ranked))
	selected := []rankedEvent{}
	seen := map[string]bool{}
	for range ranked {
		for _, caps := range [][2]bool{{true, true}, {true, false}, {false, true}, {false, false}} {
			best := chooseDiverse(ranked, used, selected, seen, false, caps[0], caps[1])
			discovery := chooseDiverse(ranked, used, selected, seen, true, caps[0], caps[1])
			if discovery >= 0 && (best < 0 || ranked[discovery].groupScore < ranked[best].groupScore-categoryDiscoveryMaxScoreDrop) {
				t.Fatal("category discovery escaped the relevance band")
			}
		}
		next := got[len(selected)]
		for i := range ranked {
			if ranked[i].event.ID == next.event.ID {
				used[i] = true
			}
		}
		selected = append(selected, next)
		seen[next.primary] = true
	}
}

func TestColdStartExplicitIntentAndPairFairness(t *testing.T) {
	price := pgtype.Int4{Int32: 100, Valid: true}
	music := catalog.Event{Event: platform.Event{PriceFromMinor: price}, Categories: []platform.EventCategory{{CategorySlug: "music", Weight: 1}}}
	freeOther := catalog.Event{Event: platform.Event{PriceFromMinor: pgtype.Int4{Valid: true}}, Categories: []platform.EventCategory{{CategorySlug: "sports", Weight: 1}}}
	a := normalizedIntent{budget: 100, categories: map[string]bool{"music": true}}
	matching := scoreEvent(music, platform.Venue{}, a, a)
	irrelevant := scoreEvent(freeOther, platform.Venue{}, a, a)
	if matching.groupScore <= irrelevant.groupScore {
		t.Fatal("cold-start room category must outweigh free but unrelated content")
	}
	emptyProfile := a
	emptyProfile.profile = normalizedProfile{present: true}
	if scoreEvent(music, platform.Venue{}, emptyProfile, emptyProfile).groupScore != matching.groupScore {
		t.Fatal("an absent profile is not negative evidence")
	}
	b := normalizedIntent{budget: 100, categories: map[string]bool{"sports": true}}
	compromise := music
	compromise.Categories = []platform.EventCategory{{CategorySlug: "music", Weight: .5}, {CategorySlug: "sports", Weight: .5}}
	shared, oneSided := scoreEvent(compromise, platform.Venue{}, a, b), scoreEvent(music, platform.Venue{}, a, b)
	if shared.groupScore <= oneSided.groupScore || oneSided.score.ParticipantScoreMin != 0 {
		t.Fatal("one-sided fit must not outrank a meaningful pair compromise")
	}
	want := .65*float64(shared.score.ParticipantScoreMin) + .35*float64(shared.score.ParticipantScoreMean)
	if math.Abs(shared.groupScore-want) > 1e-7 {
		t.Fatal("pair fairness aggregator changed")
	}
}

func TestUnknownPriceRemainsUnknownAndEligible(t *testing.T) {
	dataset, _ := loadEvaluation(t)
	for _, c := range dataset.Cases {
		if c.Name != "unknown_price" && c.Name != "all_unknown_price" {
			continue
		}
		builder, input, _, _ := newEvaluationBuilder(t, dataset, c)
		result, err := builder.Build(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		unknown := 0
		for _, candidate := range result.Candidates {
			if candidate.FeatureSnapshot[featurePriceKnown] != 0 {
				continue
			}
			unknown++
			if candidate.FeatureSnapshot[featureBudgetHeadroom] != 0 {
				t.Fatal("unknown price has fabricated headroom")
			}
			for _, explanation := range candidate.Explanation {
				if explanation.Code == "budget_fit" {
					t.Fatal("unknown price explained as a budget match")
				}
			}
		}
		if c.Name == "all_unknown_price" && unknown != len(c.Events) {
			t.Fatal("unknown-price catalog was discarded")
		}
		if c.Name == "unknown_price" {
			if unknown != 1 || result.Candidates[len(result.Candidates)-1].FeatureSnapshot[featurePriceKnown] != 0 {
				t.Fatal("otherwise equal known affordable prices should precede unknown")
			}
		}
	}
}

type sharedBehaviorFixture map[uuid.UUID][]contracts.BehavioralCategoryCount

func (f sharedBehaviorFixture) LoadBehavioralCategoryCounts(context.Context, []uuid.UUID) (map[uuid.UUID][]contracts.BehavioralCategoryCount, error) {
	return f, nil
}

func TestBehaviorRequiresEvidenceAndDoesNotMutateHistory(t *testing.T) {
	id := evaluationID("participant", 1)
	for _, votes := range []int64{0, 1, 2, 4, 5, 20} {
		for _, negative := range []bool{false, true} {
			count := contracts.BehavioralCategoryCount{CategorySlug: "music", Likes: votes}
			if negative {
				count.Likes, count.Dislikes = 0, votes
			}
			builder := &PoolBuilder{behavior: sharedBehaviorFixture{id: {count}}}
			got, err := builder.loadBehavior(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			value := got[id].categories["music"]
			if votes < 5 && (value != 0 || got[id].digest != "") {
				t.Fatalf("%d votes affected ranking", votes)
			}
			if votes >= 5 && ((negative && value >= 0) || (!negative && value <= 0)) {
				t.Fatalf("sufficient evidence lost: %d", votes)
			}
		}
	}
	counts := sharedBehaviorFixture{id: {
		{CategorySlug: "sports", Likes: 5}, {CategorySlug: " Music ", Likes: 2}, {CategorySlug: "music", Likes: 3},
	}}
	before, _ := json.Marshal(counts)
	builder := &PoolBuilder{behavior: counts}
	first, err := builder.loadBehavior(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(counts)
	if string(before) != string(after) {
		t.Fatal("ranker mutated loader-owned history")
	}
	slices.Reverse(counts[id])
	reordered, err := builder.loadBehavior(context.Background(), id)
	if err != nil || !reflect.DeepEqual(first, reordered) {
		t.Fatal("history order changed signal or fingerprint")
	}
	merged := &PoolBuilder{behavior: sharedBehaviorFixture{id: {{CategorySlug: "music", Likes: 5}, {CategorySlug: "sports", Likes: 5}}}}
	canonical, err := merged.loadBehavior(context.Background(), id)
	if err != nil || !reflect.DeepEqual(first, canonical) {
		t.Fatal("category aliases depend on record splitting")
	}
	for _, invalid := range [][]contracts.BehavioralCategoryCount{
		{{CategorySlug: "music", Likes: math.MaxInt64, Dislikes: 1}},
		{{CategorySlug: "music", Likes: math.MaxInt64}, {CategorySlug: "music", Likes: 1}},
	} {
		builder.behavior = sharedBehaviorFixture{id: invalid}
		if _, err := builder.loadBehavior(context.Background(), id); err == nil {
			t.Fatal("overflowing history was accepted")
		}
	}
}

func TestSeriesRepresentativeWithEqualStartIsDeterministic(t *testing.T) {
	a := seriesTestEvent(evaluationID("event", 1), "synthetic", "same-event", "2026-10-24T10:00:00Z")
	b := seriesTestEvent(evaluationID("event", 2), "synthetic", "same-event", "2026-10-24T10:00:00Z")
	first, reversed := collapseEventSeries([]catalog.Event{a, b}), collapseEventSeries([]catalog.Event{b, a})
	if !reflect.DeepEqual(first, reversed) || len(first) != 1 || first[0].OtherOccurrences != 1 {
		t.Fatal("equal-time series representative depends on catalog order")
	}
	a.ExternalID, b.ExternalID = "", ""
	if len(collapseEventSeries([]catalog.Event{a, b})) != 2 {
		t.Fatal("events without verified identity collapsed")
	}
}

func TestTop24KeepsRelevantEventsWhenCapsAllow(t *testing.T) {
	dataset, _ := loadEvaluation(t)
	intent := evaluationIntent{Categories: []string{"music", "cinema", "theatre", "sports"}, Budget: 10000, Dates: []string{"2026-10-24"}, Slots: []string{"day"}}
	c := evaluationCase{Name: "top24_relevance", A: intent, B: intent}
	price := int32(1000)
	for i := 1; i <= 30; i++ {
		fit, category := [2]float64{1, 1}, intent.Categories[(i-1)%4]
		if i > 24 {
			fit, category = [2]float64{0, 0}, "unrelated"
		}
		c.Events = append(c.Events, evaluationEvent{ID: i, Primary: category, Fit: fit, Price: &price,
			Starts: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC), Venue: (i-1)%3 + 1, Eligible: true})
		c.ExpectedRepresentatives = append(c.ExpectedRepresentatives, i)
	}
	got := runEvaluation(t, dataset, c)
	checkEvaluation(t, c, got)
	for _, row := range got.Rows {
		if row.Event > 24 {
			t.Fatal("irrelevant event displaced relevant event without a cap constraint")
		}
	}
	metrics := measureEvaluation(c, got, 24)
	if metrics.RelevantRecall == nil || *metrics.RelevantRecall != 1 {
		t.Fatal("top24 lost relevant eligible events")
	}
}
