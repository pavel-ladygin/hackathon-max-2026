package recommendations_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

type rankingProfileLoader struct {
	values map[uuid.UUID]contracts.RankingPreferences
}

func (l *rankingProfileLoader) LoadRankingPreferences(_ context.Context, userID uuid.UUID) (contracts.RankingPreferences, bool, error) {
	value, found := l.values[userID]
	return value, found, nil
}

func profileBuilder(t *testing.T, snapshot catalog.Snapshot, loader contracts.RankingPreferencesLoader) *recommendations.PoolBuilder {
	t.Helper()
	builder, err := recommendations.NewPoolBuilderWithClock(&fakeCatalog{snapshot: snapshot}, testPoolKey, func() time.Time { return testPoolNow }, loader)
	if err != nil {
		t.Fatal(err)
	}
	return builder
}

func TestRankingComponentsAndWeightedGroupScore(t *testing.T) {
	city, venue := uuid.New(), uuid.New()
	eid := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	s := catalog.Snapshot{City: platform.City{ID: city, Timezone: "UTC", CenterLat: 0, CenterLng: 0}, Venues: []platform.Venue{{ID: venue, CityID: city, Latitude: coordinate(0), Longitude: coordinate(0)}}, Events: []catalog.Event{event(eid, venue, "2026-09-20T13:00:00Z", 500, true, "published")}}
	s.Events[0].Categories = []platform.EventCategory{{EventID: eid, CategorySlug: "theatre", Weight: 0.8, IsPrimary: true}}
	in := validInput(city)
	in.FirstIntent.CategorySlugs, in.SecondIntent.CategorySlugs = []string{"theatre"}, []string{"cinema"}
	in.FirstIntent.BudgetMaxMinor, in.SecondIntent.BudgetMaxMinor = 1000, 1000
	r := int32(1000)
	in.FirstIntent.RadiusM, in.SecondIntent.RadiusM = &r, nil
	in.FirstIntent.Location = &contracts.GeoPoint{Latitude: 0, Longitude: 0}
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(got.Candidates) != 1 {
		t.Fatalf("Build: err=%v candidates=%d", err, len(got.Candidates))
	}
	c := got.Candidates[0]
	for _, key := range []string{"category_affinity", "current_intent_category_fit", "time_quality", "budget_headroom", "distance_quality", "novelty", "popularity"} {
		if _, ok := c.FeatureSnapshot[key]; !ok {
			t.Fatalf("missing feature %q: %+v", key, c.FeatureSnapshot)
		}
	}
	wantFeatures := map[string]float64{
		"category_affinity": 0, "current_intent_category_fit": .4, "time_quality": 1,
		"budget_headroom": .5, "distance_quality": .5, "novelty": 1, "popularity": 0,
	}
	for key, want := range wantFeatures {
		if math.Abs(c.FeatureSnapshot[key]-want) > 1e-6 {
			t.Errorf("feature %s=%v, want %v", key, c.FeatureSnapshot[key], want)
		}
	}
	// First=.535, second=.275, mean=.405; group=.65*.275+.35*.405=.3205.
	if math.Abs(float64(c.Score.ParticipantScoreMin)-.275) > 1e-6 ||
		math.Abs(float64(c.Score.ParticipantScoreMean)-.405) > 1e-6 ||
		math.Abs(float64(c.Score.GroupScore)-.3205) > 1e-6 {
		t.Fatalf("weighted scores = %+v", c.Score)
	}
	if c.FeatureSnapshot["category_affinity"] != 0 || c.FeatureSnapshot["popularity"] != 0 {
		t.Fatalf("unexpected mean feature values: %+v", c.FeatureSnapshot)
	}
}

func TestRankingHMACTieOrderAndSecretIsolation(t *testing.T) {
	city, venue := uuid.New(), uuid.New()
	s := catalog.Snapshot{City: platform.City{ID: city, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venue, CityID: city}}, Events: nil}
	in := validInput(city)
	for i := 1; i <= 8; i++ {
		id := uuid.UUID{15: byte(i)}
		s.Events = append(s.Events, event(id, venue, "2026-09-20T13:00:00Z", 0, true, "published"))
	}
	a, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	shuffled := s
	sort.Slice(shuffled.Events, func(i, j int) bool { return shuffled.Events[i].ID.String() > shuffled.Events[j].ID.String() })
	b, err := newBuilder(t, &fakeCatalog{snapshot: shuffled}).Build(context.Background(), in)
	if err != nil || !reflect.DeepEqual(candidateIDs(a.Candidates), candidateIDs(b.Candidates)) {
		t.Fatalf("catalog order changed tie result: %v %v", err, candidateIDs(b.Candidates))
	}
	want := append([]uuid.UUID(nil), candidateIDs(a.Candidates)...)
	sort.Slice(want, func(i, j int) bool {
		return tieDigest(testPoolKey, in.RoomID, in.PoolVersion, want[i]) < tieDigest(testPoolKey, in.RoomID, in.PoolVersion, want[j])
	})
	if !reflect.DeepEqual(candidateIDs(a.Candidates), want) {
		t.Fatalf("pool order is not HMAC order: got=%v want=%v", candidateIDs(a.Candidates), want)
	}
	otherKey := []byte("fedcba9876543210fedcba9876543210")
	c, err := newBuilderWithKey(t, &fakeCatalog{snapshot: s}, otherKey).Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(candidateIDs(a.Candidates), candidateIDs(c.Candidates)) {
		t.Fatalf("changing tie secret did not change an all-tie order")
	}
}

func tieDigest(key []byte, room uuid.UUID, version int32, event uuid.UUID) string {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(room.String() + "|" + fmt.Sprint(version) + "|" + event.String()))
	return hex.EncodeToString(h.Sum(nil))
}

func TestRankingCategoryPreferenceChangesOrderButDoesNotFilter(t *testing.T) {
	city, venue := uuid.New(), uuid.New()
	aid, bid := uuid.UUID{15: 1}, uuid.UUID{15: 2}
	s := catalog.Snapshot{City: platform.City{ID: city, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venue, CityID: city}}, Events: []catalog.Event{event(aid, venue, "2026-09-20T13:00:00Z", 0, true, "published"), event(bid, venue, "2026-09-20T14:00:00Z", 0, true, "published")}}
	s.Events[0].Categories = []platform.EventCategory{{EventID: aid, CategorySlug: "theatre", Weight: 1}}
	s.Events[1].Categories = []platform.EventCategory{{EventID: bid, CategorySlug: "cinema", Weight: 1}}
	in := validInput(city)
	in.FirstIntent.CategorySlugs, in.SecondIntent.CategorySlugs = []string{"theatre"}, []string{"theatre"}
	a, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(a.Candidates) != 2 || a.Candidates[0].EventID != aid {
		t.Fatalf("matching category should rank first: %+v err=%v", a.Candidates, err)
	}
	in.FirstIntent.CategorySlugs, in.SecondIntent.CategorySlugs = []string{"opera"}, []string{"opera"}
	b, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(b.Candidates) != 2 {
		t.Fatalf("unmatched categories must remain eligible: %+v err=%v", b.Candidates, err)
	}
}

func TestPermanentPreferencesFeedPersonalAndGroupRanking(t *testing.T) {
	city, venue := uuid.New(), uuid.New()
	standupID, theatreID := uuid.UUID{15: 1}, uuid.UUID{15: 2}
	snapshot := catalog.Snapshot{
		City:   platform.City{ID: city, Timezone: "UTC"},
		Venues: []platform.Venue{{ID: venue, CityID: city}},
		Events: []catalog.Event{
			event(standupID, venue, "2026-09-20T13:00:00Z", 0, true, "published"),
			event(theatreID, venue, "2026-09-20T14:00:00Z", 0, true, "published"),
		},
	}
	snapshot.Events[0].Categories = []platform.EventCategory{{EventID: standupID, CategorySlug: "standup", Weight: 1, IsPrimary: true}}
	snapshot.Events[1].Categories = []platform.EventCategory{{EventID: theatreID, CategorySlug: "theatre", Weight: 1, IsPrimary: true}}
	input := validInput(city)
	input.FirstIntent.UserID, input.SecondIntent.UserID = uuid.New(), uuid.New()
	loader := &rankingProfileLoader{values: map[uuid.UUID]contracts.RankingPreferences{
		input.FirstIntent.UserID: {CityID: city, InterestSlugs: []string{"standup"}, BudgetMaxMinor: 1000, UsualDayTypes: []string{"weekend"}, UsualTimeSlots: []string{"day"}, Version: 1},
	}}
	builder := profileBuilder(t, snapshot, loader)

	preferred, err := builder.Build(context.Background(), input)
	if err != nil || preferred.Candidates[0].EventID != standupID || preferred.Candidates[0].FeatureSnapshot["category_affinity"] <= 0 {
		t.Fatalf("standup profile did not uplift standup: candidates=%+v err=%v", preferred.Candidates, err)
	}
	if !hasExplanation(preferred.Candidates[0], "profile_affinity") || hasExplanation(preferred.Candidates[1], "profile_affinity") {
		t.Fatalf("profile explanation is not evidence-based: %+v", preferred.Candidates)
	}

	current := input
	current.FirstIntent.CategorySlugs = []string{"theatre"}
	current.SecondIntent.CategorySlugs = []string{"theatre"}
	withCurrent, err := builder.Build(context.Background(), current)
	if err != nil || withCurrent.Candidates[0].EventID != theatreID {
		t.Fatalf("current theatre intent did not outrank old standup preference: candidates=%+v err=%v", withCurrent.Candidates, err)
	}

	loader.values[input.SecondIntent.UserID] = contracts.RankingPreferences{
		CityID: city, InterestSlugs: []string{"theatre"}, BudgetMaxMinor: 1000, Version: 1,
	}
	split, err := builder.Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	standup := candidateByID(t, split.Candidates, standupID)
	if standup.Score.ParticipantScoreMin >= standup.Score.ParticipantScoreMean {
		t.Fatalf("separate participant profiles were not preserved in group score: %+v", standup.Score)
	}

	repeated, err := builder.Build(context.Background(), input)
	if err != nil || !reflect.DeepEqual(split, repeated) {
		t.Fatalf("profile ranking is not deterministic: err=%v", err)
	}
	beforeFingerprint := split.InputFingerprint
	changed := loader.values[input.FirstIntent.UserID]
	changed.InterestSlugs = []string{"walks"}
	changed.Version++
	loader.values[input.FirstIntent.UserID] = changed
	afterPreferenceChange, err := builder.Build(context.Background(), input)
	if err != nil || afterPreferenceChange.InputFingerprint == beforeFingerprint {
		t.Fatalf("relevant preference change did not change fingerprint: err=%v", err)
	}
}

func TestMissingPermanentPreferencesPreserveBehavior(t *testing.T) {
	city, venue := uuid.New(), uuid.New()
	id := uuid.UUID{15: 1}
	snapshot := catalog.Snapshot{City: platform.City{ID: city, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venue, CityID: city}}, Events: []catalog.Event{event(id, venue, "2026-09-20T13:00:00Z", 0, true, "published")}}
	input := validInput(city)
	input.FirstIntent.UserID, input.SecondIntent.UserID = uuid.New(), uuid.New()
	withoutLoader, err := newBuilder(t, &fakeCatalog{snapshot: snapshot}).Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	missingLoader, err := profileBuilder(t, snapshot, &rankingProfileLoader{values: map[uuid.UUID]contracts.RankingPreferences{}}).Build(context.Background(), input)
	if err != nil || !reflect.DeepEqual(withoutLoader, missingLoader) {
		t.Fatalf("missing profiles changed safe behavior: err=%v without=%+v with=%+v", err, withoutLoader, missingLoader)
	}
}

func TestPermanentProfileFallbackUsesAllStoredFields(t *testing.T) {
	city, venue, eventID := uuid.New(), uuid.New(), uuid.UUID{15: 1}
	e := event(eventID, venue, "2026-09-20T13:00:00Z", 800, true, "published")
	e.Categories = []platform.EventCategory{{EventID: eventID, CategorySlug: "standup", Weight: 1, IsPrimary: true}}
	snapshot := catalog.Snapshot{City: platform.City{ID: city, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venue, CityID: city}}, Events: []catalog.Event{e}}
	input := validInput(city)
	input.FirstIntent.UserID, input.SecondIntent.UserID = uuid.New(), uuid.New()
	input.FirstIntent.TimeSlots, input.SecondIntent.TimeSlots = nil, nil
	profile := contracts.RankingPreferences{
		CityID: city, InterestSlugs: []string{"standup"}, BudgetMaxMinor: 2000,
		UsualDayTypes: []string{"weekend"}, UsualTimeSlots: []string{"day"}, Version: 3,
	}
	loader := &rankingProfileLoader{values: map[uuid.UUID]contracts.RankingPreferences{
		input.FirstIntent.UserID: profile, input.SecondIntent.UserID: profile,
	}}
	withProfile, err := profileBuilder(t, snapshot, loader).Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	features := withProfile.Candidates[0].FeatureSnapshot
	for key, want := range map[string]float64{"category_affinity": .5, "time_quality": .5, "budget_headroom": .3} {
		if math.Abs(features[key]-want) > 1e-6 {
			t.Errorf("feature %s=%v, want %v", key, features[key], want)
		}
	}

	wrongCity := profile
	wrongCity.CityID = uuid.New()
	loader.values[input.FirstIntent.UserID], loader.values[input.SecondIntent.UserID] = wrongCity, wrongCity
	ignored, err := profileBuilder(t, snapshot, loader).Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	ignoredFeatures := ignored.Candidates[0].FeatureSnapshot
	if ignoredFeatures["category_affinity"] != 0 || ignoredFeatures["time_quality"] != 0 || math.Abs(ignoredFeatures["budget_headroom"]-.2) > 1e-6 {
		t.Fatalf("preferences from another city affected ranking: %+v", ignoredFeatures)
	}
	if ignored.InputFingerprint == withProfile.InputFingerprint {
		t.Fatal("removing city-relevant preferences did not change fingerprint")
	}
}

func hasExplanation(candidate contracts.Candidate, code string) bool {
	for _, explanation := range candidate.Explanation {
		if explanation.Code == code {
			return true
		}
	}
	return false
}

func candidateByID(t *testing.T, candidates []contracts.Candidate, id uuid.UUID) contracts.Candidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.EventID == id {
			return candidate
		}
	}
	t.Fatalf("candidate %s not found", id)
	return contracts.Candidate{}
}

func TestRankingDiversityCapAndFourCategoryCoverage(t *testing.T) {
	city, venue := uuid.New(), uuid.New()
	s := catalog.Snapshot{City: platform.City{ID: city, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venue, CityID: city}}}
	in := validInput(city)
	for i := 0; i < 24; i++ {
		id := uuid.UUID{15: byte(i + 1)}
		s.Events = append(s.Events, event(id, venue, "2026-09-20T13:00:00Z", 0, true, "published"))
		s.Events[i].Categories = []platform.EventCategory{{EventID: id, CategorySlug: []string{"theatre", "cinema", "sports", "food"}[i%4], Weight: 1, IsPrimary: true}}
	}
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(got.Candidates) != 24 {
		t.Fatalf("max pool semantics: n=%d err=%v", len(got.Candidates), err)
	}
	seen := map[string]bool{}
	for _, c := range got.Candidates[:4] {
		for _, e := range s.Events {
			if e.ID == c.EventID {
				seen[e.Categories[0].CategorySlug] = true
			}
		}
	}
	if len(seen) != 4 {
		t.Fatalf("expected four primary categories in first four positions, got %v", seen)
	}
	for i := 3; i < len(got.Candidates); i++ {
		// No category should occupy more than three consecutive positions when alternatives exist.
		var a, b, c string
		for _, e := range s.Events {
			if e.ID == got.Candidates[i-2].EventID {
				a = e.Categories[0].CategorySlug
			}
			if e.ID == got.Candidates[i-1].EventID {
				b = e.Categories[0].CategorySlug
			}
			if e.ID == got.Candidates[i].EventID {
				c = e.Categories[0].CategorySlug
			}
		}
		if i >= 3 {
			var d string
			for _, e := range s.Events {
				if e.ID == got.Candidates[i-3].EventID {
					d = e.Categories[0].CategorySlug
				}
			}
			if d == a && a == b && b == c {
				alternativeRemains := false

				for j := i + 1; j < len(got.Candidates); j++ {
					for _, e := range s.Events {
						if e.ID == got.Candidates[j].EventID &&
							e.Categories[0].CategorySlug != c {
							alternativeRemains = true
							break
						}
					}
					if alternativeRemains {
						break
					}
				}

				if alternativeRemains {
					t.Fatalf("category run exceeds three at %d while an alternative category remains", i)
				}
			}
		}
	}
}

func TestRankingDiversityVenueCapAndDeterministicRelaxation(t *testing.T) {
	city, firstVenue, secondVenue := uuid.New(), uuid.New(), uuid.New()
	s := catalog.Snapshot{
		City:   platform.City{ID: city, Timezone: "UTC"},
		Venues: []platform.Venue{{ID: firstVenue, CityID: city}, {ID: secondVenue, CityID: city}},
	}
	in := validInput(city)
	in.FirstIntent.CategorySlugs, in.SecondIntent.CategorySlugs = []string{"theatre"}, []string{"theatre"}
	for i := 0; i < 6; i++ {
		venue := firstVenue
		if i >= 3 {
			venue = secondVenue
		}
		id := uuid.UUID{15: byte(i + 1)}
		e := event(id, venue, "2026-09-20T13:00:00Z", 0, true, "published")
		if i < 3 {
			e.Categories = []platform.EventCategory{{EventID: id, CategorySlug: "theatre", Weight: 1}}
		}
		s.Events = append(s.Events, e)
	}
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	venues := map[uuid.UUID]uuid.UUID{}
	for _, e := range s.Events {
		venues[e.ID] = e.VenueID
	}
	for i := 2; i < len(got.Candidates); i++ {
		a, b, c := venues[got.Candidates[i-2].EventID], venues[got.Candidates[i-1].EventID], venues[got.Candidates[i].EventID]
		if a == b && b == c {
			t.Fatalf("venue cap was relaxed while another venue was available at position %d", i)
		}
	}

	// With only one venue the cap must relax without dropping eligible events.
	s.Venues = s.Venues[:1]
	s.Events = s.Events[:3]
	relaxed, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(relaxed.Candidates) != 3 {
		t.Fatalf("single-venue relaxation: count=%d err=%v", len(relaxed.Candidates), err)
	}
}

func TestRankingExplanationAndFingerprintPrivacy(t *testing.T) {
	city, venue := uuid.New(), uuid.New()
	id := uuid.UUID{15: 1}
	s := catalog.Snapshot{City: platform.City{ID: city, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venue, CityID: city, Latitude: coordinate(42.123456), Longitude: coordinate(43.654321)}}, Events: []catalog.Event{event(id, venue, "2026-09-20T13:00:00Z", 0, true, "published")}}
	s.Events[0].Categories = []platform.EventCategory{{EventID: id, CategorySlug: "theatre", Weight: 1, IsPrimary: true}}
	in := validInput(city)
	in.FirstIntent.UserID = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	in.SecondIntent.UserID = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	in.FirstIntent.BudgetMaxMinor, in.SecondIntent.BudgetMaxMinor = 777777, 888888
	in.FirstIntent.Location = &contracts.GeoPoint{Latitude: 42.123456, Longitude: 43.654321}
	in.SecondIntent.Location = &contracts.GeoPoint{Latitude: 42.123456, Longitude: 43.654321}
	radius := int32(1000)
	in.FirstIntent.RadiusM, in.SecondIntent.RadiusM = &radius, &radius
	in.FirstIntent.CategorySlugs, in.SecondIntent.CategorySlugs = []string{"theatre"}, []string{"theatre"}
	secret := "private phrase 940287"
	in.FirstIntent.FreeText = &secret
	a, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Candidates[0].Explanation) != 3 {
		t.Fatalf("explanation count=%d, want capped at 3", len(a.Candidates[0].Explanation))
	}
	allowed := map[string]bool{"time_fit": true, "budget_fit": true, "shared_category": true, "nearby": true, "profile_affinity": true, "popular": true}
	encoded, _ := json.Marshal(a)
	for _, privateValue := range []string{secret, in.FirstIntent.UserID.String(), in.SecondIntent.UserID.String(), "777777", "888888", "42.123456", "43.654321"} {
		if strings.Contains(string(encoded), privateValue) {
			t.Fatalf("result exposes private input %q", privateValue)
		}
	}
	for _, x := range a.Candidates[0].Explanation {
		if x.Text == "" || !allowed[x.Code] {
			t.Fatalf("unsafe explanation: %+v", x)
		}
	}
	in.FirstIntent.FreeText = nil
	b, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if a.InputFingerprint != b.InputFingerprint {
		t.Fatal("free text must not affect fingerprint")
	}
	normalized := in
	normalized.FirstIntent.CategorySlugs = []string{"sports", "theatre", "sports"}
	normalized.SecondIntent.CategorySlugs = []string{"cinema", "food"}
	withCategories, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), normalized)
	if err != nil {
		t.Fatal(err)
	}
	reordered := normalized
	reordered.FirstIntent.CategorySlugs = []string{"theatre", "sports"}
	reordered.SecondIntent.CategorySlugs = []string{"food", "cinema", "food"}
	reorderedResult, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), reordered)
	if err != nil || withCategories.InputFingerprint != reorderedResult.InputFingerprint {
		t.Fatalf("category set normalization changed fingerprint: err=%v", err)
	}
	changedCategories := reordered
	changedCategories.FirstIntent.CategorySlugs = []string{"walks"}
	changed, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), changedCategories)
	if err != nil || changed.InputFingerprint == reorderedResult.InputFingerprint {
		t.Fatalf("category change did not change fingerprint: err=%v", err)
	}
	otherKey := []byte("fedcba9876543210fedcba9876543210")
	otherKeyResult, err := newBuilderWithKey(t, &fakeCatalog{snapshot: s}, otherKey).Build(context.Background(), reordered)
	if err != nil || otherKeyResult.InputFingerprint == reorderedResult.InputFingerprint {
		t.Fatalf("tie-break key change did not change fingerprint: err=%v", err)
	}
}

func TestRankingHardFiltersBeatSoftScoreAndPreviousIDs(t *testing.T) {
	city, venue := uuid.New(), uuid.New()
	good, expensive, previous := uuid.UUID{15: 1}, uuid.UUID{15: 2}, uuid.UUID{15: 3}
	s := catalog.Snapshot{City: platform.City{ID: city, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venue, CityID: city}}, Events: []catalog.Event{
		event(good, venue, "2026-09-20T13:00:00Z", 0, true, "published"),
		event(expensive, venue, "2026-09-20T13:00:00Z", 9999, true, "published"),
		event(previous, venue, "2026-09-20T13:00:00Z", 0, true, "published"),
	}}
	s.Events[1].Categories = []platform.EventCategory{{EventID: expensive, CategorySlug: "theatre", Weight: 1, IsPrimary: true}}
	in := validInput(city)
	in.FirstIntent.CategorySlugs, in.SecondIntent.CategorySlugs = []string{"theatre"}, []string{"theatre"}
	in.PreviousEventIDs = []uuid.UUID{previous}
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(got.Candidates) != 1 || got.Candidates[0].EventID != good {
		t.Fatalf("hard eligibility/previous exclusion failed: %+v err=%v", got.Candidates, err)
	}
}

func TestRankingPoolSizeBoundaries(t *testing.T) {
	for _, n := range []int{19, 20, 21, 24, 25} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			city, venue := uuid.New(), uuid.New()
			s := catalog.Snapshot{City: platform.City{ID: city, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venue, CityID: city}}}
			for i := 0; i < n; i++ {
				s.Events = append(s.Events, event(uuid.UUID{15: byte(i + 1)}, venue, "2026-09-20T13:00:00Z", 0, true, "published"))
			}
			got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), validInput(city))
			if err != nil || len(got.Candidates) != minInt(n, 24) || got.IsSmall != (n <= 2) {
				t.Fatalf("n=%d count=%d small=%v err=%v", n, len(got.Candidates), got.IsSmall, err)
			}
		})
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestNewPoolBuilderRejectsShortKey(t *testing.T) {
	if _, err := recommendations.NewPoolBuilder(&fakeCatalog{}, []byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}
