package recommendations_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

var testPoolKey = []byte("0123456789abcdef0123456789abcdef")

func newBuilder(t *testing.T, c *fakeCatalog) *recommendations.PoolBuilder {
	return newBuilderWithKey(t, c, testPoolKey)
}

func newBuilderWithKey(t *testing.T, c *fakeCatalog, key []byte) *recommendations.PoolBuilder {
	t.Helper()
	b, err := recommendations.NewPoolBuilder(c, key)
	if err != nil {
		t.Fatalf("new pool builder: %v", err)
	}
	return b
}

type fakeCatalog struct {
	snapshot catalog.Snapshot
	err      error
	seen     uuid.UUID
}

func (f *fakeCatalog) LoadCity(ctx context.Context, cityID uuid.UUID) (catalog.Snapshot, error) {
	f.seen = cityID
	if err := ctx.Err(); err != nil {
		return catalog.Snapshot{}, err
	}
	if f.err != nil {
		return catalog.Snapshot{}, f.err
	}
	return f.snapshot, nil
}

func TestPoolBuilderHardFiltersAndDeterministicOrdering(t *testing.T) {
	cityID := uuid.New()
	venueID := uuid.New()
	snapshot := catalog.Snapshot{
		City:          platform.City{ID: cityID, Timezone: "Europe/Moscow", CenterLat: 55.75, CenterLng: 37.61},
		Venues:        []platform.Venue{{ID: venueID, CityID: cityID, Latitude: 55.75, Longitude: 37.61}},
		MetroStations: []platform.MetroStation{{ID: uuid.New(), CityID: cityID, Latitude: 55.750, Longitude: 37.610}},
	}
	base := contracts.BuildInput{
		RoomID: uuid.New(), CityID: cityID, RoundNo: 1, PoolVersion: 2,
		FirstIntent:  contracts.ParticipantIntent{Dates: []string{"2026-09-20"}, TimeSlots: []string{"day"}, BudgetMaxMinor: 2000},
		SecondIntent: contracts.ParticipantIntent{Dates: []string{"2026-09-20"}, TimeSlots: []string{"day"}, BudgetMaxMinor: 2000},
	}
	valid := event(uuid.UUID{15: 2}, venueID, "2026-09-20T13:00:00+03:00", 1000, true, "published")
	valid.Categories = []platform.EventCategory{{EventID: valid.ID, CategorySlug: "theatre"}}
	other := event(uuid.New(), venueID, "2026-09-20T14:00:00+03:00", 1500, true, "published")
	other.Categories = []platform.EventCategory{{EventID: other.ID, CategorySlug: "completely-unrelated"}}
	tie := event(uuid.UUID{15: 1}, venueID, "2026-09-20T13:00:00+03:00", 1000, true, "published")
	snapshot.Events = []catalog.Event{other, valid, tie}
	base.FirstIntent.CategorySlugs = []string{"sports"}
	base.SecondIntent.CategorySlugs = []string{"cinema"}
	fake := &fakeCatalog{snapshot: snapshot}
	result, err := newBuilder(t, fake).Build(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateIDs(result.Candidates); len(got) != 3 || !contains(got, tie.ID) || !contains(got, valid.ID) || !contains(got, other.ID) {
		t.Fatalf("all eligible events should remain in pool: got %v", got)
	}
	for _, candidate := range result.Candidates {
		if candidate.Score.GroupScore <= 0 || len(candidate.Explanation) == 0 || len(candidate.FeatureSnapshot) == 0 {
			t.Fatalf("candidate %s lacks ranking output: %+v", candidate.EventID, candidate)
		}
	}
	if result.RankerVersion != "scoring-diversity-v3-behavior" {
		t.Fatalf("ranker version = %q", result.RankerVersion)
	}
	slices.Reverse(fake.snapshot.Events)
	base.FirstIntent.CategorySlugs = []string{"food"}
	base.SecondIntent.CategorySlugs = []string{"walks"}
	text := "private text should not affect hard filters"
	base.FirstIntent.FreeText = &text
	base.FirstIntent.Location = &contracts.GeoPoint{Latitude: 89, Longitude: 179}
	repeated, err := newBuilder(t, fake).Build(context.Background(), base)
	if err != nil || reflect.DeepEqual(result, repeated) {
		t.Fatalf("category preferences should affect ranking: err=%v", err)
	}
	if fake.seen != cityID {
		t.Fatal("builder did not request the input city")
	}
}

func TestPoolBuilderBoundaryFilters(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	base := contracts.BuildInput{RoomID: uuid.New(), CityID: cityID, RoundNo: 1, PoolVersion: 1,
		FirstIntent:  contracts.ParticipantIntent{Dates: []string{"2026-09-20"}, TimeSlots: []string{"morning"}, BudgetMaxMinor: 1000},
		SecondIntent: contracts.ParticipantIntent{Dates: []string{"2026-09-20"}, TimeSlots: []string{"morning"}, BudgetMaxMinor: 1000}}
	baseSnapshot := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "Europe/Moscow", CenterLat: 55.75, CenterLng: 37.61}, Venues: []platform.Venue{{ID: venueID, CityID: cityID, Latitude: 55.75, Longitude: 37.61}}}
	cases := []struct {
		name string
		when string
		slot string
	}{
		{"night end", "2026-09-20T05:59:59+03:00", "night"},
		{"morning start", "2026-09-20T06:00:00+03:00", "morning"},
		{"morning end", "2026-09-20T11:59:59+03:00", "morning"},
		{"day start", "2026-09-20T12:00:00+03:00", "day"},
		{"day end", "2026-09-20T16:59:59+03:00", "day"},
		{"evening start", "2026-09-20T17:00:00+03:00", "evening"},
		{"evening end", "2026-09-20T21:59:59+03:00", "evening"},
		{"night start", "2026-09-20T22:00:00+03:00", "night"},
		{"midnight", "2026-09-20T00:00:00+03:00", "night"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := baseSnapshot
			s.Events = []catalog.Event{event(uuid.New(), venueID, tc.when, 1000, true, "published")}
			for _, slot := range []string{"morning", "day", "evening", "night"} {
				in := base
				in.FirstIntent.TimeSlots, in.SecondIntent.TimeSlots = []string{slot}, []string{slot}
				got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
				if err != nil || (len(got.Candidates) == 1) != (slot == tc.slot) {
					t.Fatalf("slot=%s candidate count=%d, want slot=%s, err=%v", slot, len(got.Candidates), tc.slot, err)
				}
			}
		})
	}
}

func TestPoolBuilderExclusionsUnionAndBudgetSemantics(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	otherVenue := uuid.New()
	s := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "Europe/Moscow", CenterLat: 55.75, CenterLng: 37.61}, Venues: []platform.Venue{
		{ID: venueID, CityID: cityID, Latitude: 55.75, Longitude: 37.61, VenueType: "nightclub"},
		{ID: otherVenue, CityID: cityID, Latitude: 55.75, Longitude: 37.61},
	}}
	base := contracts.BuildInput{RoomID: uuid.New(), CityID: cityID,
		FirstIntent:  contracts.ParticipantIntent{Dates: []string{"2026-09-20"}, TimeSlots: []string{"day"}, BudgetMaxMinor: 1000, ExclusionSlugs: []string{"nightclubs", "very_loud", "outdoor"}},
		SecondIntent: contracts.ParticipantIntent{Dates: []string{"2026-09-20"}, TimeSlots: []string{"day"}, BudgetMaxMinor: 1000, ExclusionSlugs: []string{"nightclubs", "very_loud", "outdoor"}}}
	free := event(uuid.New(), otherVenue, "2026-09-20T13:00:00+03:00", 0, true, "published")
	free.TicketUrl = pgtype.Text{String: "https://tickets.example/free", Valid: true}
	badVenue := event(uuid.New(), venueID, "2026-09-20T13:00:00+03:00", 500, true, "published")
	badVenue.LoudnessLevel = pgtype.Text{String: "very_loud", Valid: true}
	badStatus := event(uuid.New(), otherVenue, "2026-09-20T13:00:00+03:00", 500, true, "cancelled")
	badTicket := event(uuid.New(), otherVenue, "2026-09-20T13:00:00+03:00", 500, false, "published")
	nullPrice := event(uuid.New(), otherVenue, "2026-09-20T13:00:00+03:00", 500, true, "published")
	nullPrice.PriceFromMinor = pgtype.Int4{}
	outdoor := event(uuid.New(), otherVenue, "2026-09-20T13:00:00+03:00", 500, true, "published")
	outdoor.Indoor = pgtype.Bool{Bool: false, Valid: true}
	s.Events = []catalog.Event{badVenue, badStatus, badTicket, free, outdoor}
	s.Events = append(s.Events, nullPrice)
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].EventID != free.ID {
		t.Fatalf("eligible free event was not retained: %+v", got.Candidates)
	}
}

func TestPoolBuilderExcludesDemoAndEventsWithoutUsableTicketURL(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	snapshot := catalog.Snapshot{
		City:   platform.City{ID: cityID, Timezone: "UTC"},
		Venues: []platform.Venue{{ID: venueID, CityID: cityID}},
	}
	eligible := event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published")
	demo := event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published")
	demo.IsDemo = true
	missingURL := event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published")
	missingURL.TicketUrl = pgtype.Text{}
	emptyURL := event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published")
	emptyURL.TicketUrl = pgtype.Text{Valid: true}
	snapshot.Events = []catalog.Event{eligible, demo, missingURL, emptyURL}

	result, err := newBuilder(t, &fakeCatalog{snapshot: snapshot}).Build(context.Background(), validInput(cityID))
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateIDs(result.Candidates); len(got) != 1 || got[0] != eligible.ID {
		t.Fatalf("only non-demo event with a usable ticket URL should remain: %v", got)
	}
}

func TestPoolBuilderPreviousIDsBeforeCapAndFingerprintNormalization(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	s := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "UTC", CenterLat: 0, CenterLng: 0}, Venues: []platform.Venue{{ID: venueID, CityID: cityID, Latitude: 0, Longitude: 0}}}
	input := contracts.BuildInput{RoomID: uuid.New(), CityID: cityID, RoundNo: 3, PoolVersion: 4,
		FirstIntent:  contracts.ParticipantIntent{Dates: []string{"2026-09-20", "2026-09-20"}, TimeSlots: []string{"day", "day"}},
		SecondIntent: contracts.ParticipantIntent{Dates: []string{"2026-09-20"}, TimeSlots: []string{"day"}}}
	for i := 0; i < 21; i++ {
		s.Events = append(s.Events, event(uuid.UUID{15: byte(i + 1)}, venueID, "2026-09-20T13:00:00Z", 0, true, "published"))
	}
	previous := s.Events[0].ID
	input.PreviousEventIDs = []uuid.UUID{previous}
	fake := &fakeCatalog{snapshot: s}
	a, err := newBuilder(t, fake).Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Candidates) != 20 || contains(candidateIDs(a.Candidates), previous) {
		t.Fatalf("previous IDs must be removed before cap: count=%d", len(a.Candidates))
	}
	input.FirstIntent.Dates = []string{"2026-09-20"}
	input.FirstIntent.TimeSlots = []string{"day"}
	b, err := newBuilder(t, fake).Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if a.InputFingerprint != b.InputFingerprint {
		t.Fatalf("equivalent set inputs should fingerprint identically: %q != %q", a.InputFingerprint, b.InputFingerprint)
	}
}

func TestPoolBuilderPropagatesCatalogErrorAndCancellation(t *testing.T) {
	want := errors.New("catalog unavailable")
	cityID, venueID := uuid.New(), uuid.New()
	input := validInput(cityID)
	snapshot := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "UTC", CenterLat: 0, CenterLng: 0}, Venues: []platform.Venue{{ID: venueID, CityID: cityID}}, Events: []catalog.Event{event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published")}}
	if _, err := newBuilder(t, &fakeCatalog{err: want}).Build(context.Background(), input); !errors.Is(err, want) {
		t.Fatalf("catalog error = %v, want %v", err, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newBuilder(t, &fakeCatalog{snapshot: snapshot}).Build(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestPoolBuilderStatusDateAndCityBoundaries(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	s := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "Europe/Moscow", CenterLat: 55.75, CenterLng: 37.61}, Venues: []platform.Venue{{ID: venueID, CityID: cityID}}, Events: []catalog.Event{
		event(uuid.New(), venueID, "2026-09-20T23:30:00Z", 0, true, "published"), // 2026-09-21 local
		event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "sold_out"),
		event(uuid.New(), venueID, "2026-09-20T14:00:00Z", 0, true, "cancelled"),
		event(uuid.New(), venueID, "2026-09-20T15:00:00Z", 0, true, "draft"),
	}}
	in := validInput(cityID)
	in.FirstIntent.Dates, in.SecondIntent.Dates = []string{"2026-09-21"}, []string{"2026-09-21"}
	in.FirstIntent.TimeSlots, in.SecondIntent.TimeSlots = []string{"night"}, []string{"night"}
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(got.Candidates) != 1 {
		t.Fatalf("local city date/status filtering: got=%+v err=%v", got.Candidates, err)
	}
	wrongCity := s
	wrongCity.Venues = []platform.Venue{{ID: venueID, CityID: uuid.New()}}
	if got, err := newBuilder(t, &fakeCatalog{snapshot: wrongCity}).Build(context.Background(), in); err != nil || len(got.Candidates) != 0 {
		t.Fatalf("venue from another city must be excluded: got=%+v err=%v", got.Candidates, err)
	}
}

func TestPoolBuilderDisjointParticipantDateAndSlotSetsAreEmpty(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	s := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venueID, CityID: cityID}}, Events: []catalog.Event{event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published")}}
	in := validInput(cityID)
	in.FirstIntent.Dates, in.SecondIntent.Dates = []string{"2026-09-20"}, []string{"2026-09-21"}
	in.FirstIntent.TimeSlots, in.SecondIntent.TimeSlots = []string{"morning"}, []string{"evening"}
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 0 {
		t.Fatalf("disjoint participant filters must produce no candidates: %+v", got.Candidates)
	}
	in.FirstIntent.Dates, in.SecondIntent.Dates = []string{"2026-09-20"}, []string{"2026-09-20"}
	got, err = newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(got.Candidates) != 0 {
		t.Fatalf("disjoint participant slots must produce no candidates: got=%v err=%v", got.Candidates, err)
	}
}

func TestPoolBuilderDayTypesORWithinParticipantANDBetweenParticipants(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	s := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venueID, CityID: cityID}}, Events: []catalog.Event{
		event(uuid.New(), venueID, "2026-09-19T13:00:00Z", 0, true, "published"), // Saturday
		event(uuid.New(), venueID, "2026-09-21T13:00:00Z", 0, true, "published"), // Monday
	}}
	in := validInput(cityID)
	in.FirstIntent.Dates, in.SecondIntent.Dates = []string{"2026-09-19", "2026-09-21"}, []string{"2026-09-19"}
	in.FirstIntent.DayTypes, in.SecondIntent.DayTypes = []string{"weekday", "weekend"}, []string{"weekend"}
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(got.Candidates) != 1 {
		t.Fatalf("day type OR/AND filtering: got=%v err=%v", got.Candidates, err)
	}
}

func TestPoolBuilderRadiusUsesEachParticipantOriginAndInclusiveBoundary(t *testing.T) {
	cityID := uuid.New()
	near, edge, outside := uuid.New(), uuid.New(), uuid.New()
	vNear, vEdge, vOutside := uuid.New(), uuid.New(), uuid.New()
	edgeLat := latitudeForMeters(1000)
	s := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "UTC", CenterLat: 50, CenterLng: 50}, Venues: []platform.Venue{
		{ID: vNear, CityID: cityID, Latitude: 0, Longitude: 0}, {ID: vEdge, CityID: cityID, Latitude: edgeLat, Longitude: 0}, {ID: vOutside, CityID: cityID, Latitude: latitudeForMeters(1201), Longitude: 0},
	}, Events: []catalog.Event{event(near, vNear, "2026-09-20T13:00:00Z", 0, true, "published"), event(edge, vEdge, "2026-09-20T14:00:00Z", 0, true, "published"), event(outside, vOutside, "2026-09-20T15:00:00Z", 0, true, "published")}}
	in := validInput(cityID)
	radius := int32(1000)
	in.FirstIntent.RadiusM, in.SecondIntent.RadiusM = &radius, &radius
	in.FirstIntent.Location, in.SecondIntent.Location = &contracts.GeoPoint{Latitude: 0, Longitude: 0}, &contracts.GeoPoint{Latitude: 0, Longitude: 0}
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	ids := candidateIDs(got.Candidates)
	if !contains(ids, near) || !contains(ids, edge) || contains(ids, outside) {
		t.Fatalf("radius boundary filtering: got %v", ids)
	}
	in.FirstIntent.Location = &contracts.GeoPoint{Latitude: 0, Longitude: 0}
	in.SecondIntent.Location = &contracts.GeoPoint{Latitude: 50, Longitude: 50}
	got, err = newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(got.Candidates) != 0 {
		t.Fatalf("both participant radii must apply from their own origins: got=%v err=%v", got.Candidates, err)
	}
}

func TestPoolBuilderMetroDistanceAndMissingStation(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	in := validInput(cityID)
	in.FirstIntent.ExclusionSlugs, in.SecondIntent.ExclusionSlugs = []string{"far_from_metro"}, []string{"far_from_metro"}
	for _, tc := range []struct {
		name string
		lng  float64
		want int
	}{{"1199", longitudeForMeters(1199), 1}, {"1200", longitudeForMeters(1200), 1}, {"1201", longitudeForMeters(1201), 0}} {
		t.Run(tc.name, func(t *testing.T) {
			s := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venueID, CityID: cityID, Latitude: 0, Longitude: 0}}, MetroStations: []platform.MetroStation{{ID: uuid.New(), CityID: cityID, Latitude: 0, Longitude: tc.lng}}, Events: []catalog.Event{event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published")}}
			got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
			if err != nil || len(got.Candidates) != tc.want {
				t.Fatalf("metro boundary: got=%v err=%v", got.Candidates, err)
			}
		})
	}
	s := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venueID, CityID: cityID}}, Events: []catalog.Event{event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published")}}
	got, err := newBuilder(t, &fakeCatalog{snapshot: s}).Build(context.Background(), in)
	if err != nil || len(got.Candidates) != 0 {
		t.Fatalf("missing metro must be excluded when requested: got=%v err=%v", got.Candidates, err)
	}
}

func latitudeForMeters(m float64) float64 { return coordinateForMeters(m, true) }

func longitudeForMeters(m float64) float64 { return coordinateForMeters(m, false) }

func coordinateForMeters(m float64, latitude bool) float64 {
	lo, hi := 0.0, 1.0
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		var d float64
		if latitude {
			d = catalog.HaversineMeters(0, 0, mid, 0)
		} else {
			d = catalog.HaversineMeters(0, 0, 0, mid)
		}
		if d < m {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func TestPoolBuilderSmallDiagnosticsAndInputSnapshotImmutability(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	s := catalog.Snapshot{City: platform.City{ID: cityID, Timezone: "UTC"}, Venues: []platform.Venue{{ID: venueID, CityID: cityID}}}
	in := validInput(cityID)
	in.FirstIntent.Dates = []string{"2026-09-20"}
	in.SecondIntent.Dates = []string{"2026-09-20"}
	for n := 0; n <= 3; n++ {
		ss := s
		for i := 0; i < n; i++ {
			ss.Events = append(ss.Events, event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published"))
		}
		beforeInput, _ := json.Marshal(in)
		beforeSnapshot, _ := json.Marshal(ss)
		got, err := newBuilder(t, &fakeCatalog{snapshot: ss}).Build(context.Background(), in)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if len(got.Candidates) != n || got.IsSmall != (n == 1 || n == 2) {
			t.Fatalf("n=%d IsSmall=%v", n, got.IsSmall)
		}
		if n == 0 {
			if len(got.Diagnostics.Reasons) != 1 || got.Diagnostics.Reasons[0].Code != "catalog_shortage" || got.Diagnostics.Reasons[0].Text == "" {
				t.Fatalf("empty pool diagnostics = %+v", got.Diagnostics)
			}
		} else if len(got.Diagnostics.Reasons) != 0 {
			t.Fatal("nonempty pool has exhaustion reasons")
		}
		afterInput, _ := json.Marshal(in)
		afterSnapshot, _ := json.Marshal(ss)
		if !reflect.DeepEqual(afterInput, beforeInput) || !reflect.DeepEqual(afterSnapshot, beforeSnapshot) {
			t.Fatalf("n=%d input/snapshot mutated", n)
		}
	}
}

func TestPoolBuilderIndividualConstraints(t *testing.T) {
	type fixture struct {
		input contracts.BuildInput
		data  catalog.Snapshot
	}
	cases := []struct {
		name string
		edit func(*fixture)
		want int
	}{
		{"budget below price", func(f *fixture) { f.input.FirstIntent.BudgetMaxMinor = 999 }, 0},
		{"budget equals price", func(f *fixture) {}, 1},
		{"budget above price", func(f *fixture) {
			f.input.FirstIntent.BudgetMaxMinor = 1001
			f.input.SecondIntent.BudgetMaxMinor = 1001
		}, 1},
		{"second budget is binding", func(f *fixture) { f.input.FirstIntent.BudgetMaxMinor = 2000; f.input.SecondIntent.BudgetMaxMinor = 999 }, 0},
		{"free at zero budget", func(f *fixture) { f.data.Events[0].PriceFromMinor.Int32 = 0; f.input.SecondIntent.BudgetMaxMinor = 0 }, 1},
		{"unknown price", func(f *fixture) { f.data.Events[0].PriceFromMinor.Valid = false }, 0},
		{"sold out", func(f *fixture) { f.data.Events[0].Status = "sold_out" }, 0},
		{"cancelled", func(f *fixture) { f.data.Events[0].Status = "cancelled" }, 0},
		{"draft", func(f *fixture) { f.data.Events[0].Status = "draft" }, 0},
		{"tickets unavailable", func(f *fixture) { f.data.Events[0].TicketAvailable = false }, 0},
		{"first date rejects", func(f *fixture) { f.input.FirstIntent.Dates = []string{"2026-09-21"} }, 0},
		{"second date rejects", func(f *fixture) { f.input.SecondIntent.Dates = []string{"2026-09-21"} }, 0},
		{"date OR", func(f *fixture) { f.input.FirstIntent.Dates = []string{"2026-09-21", "2026-09-20"} }, 1},
		{"weekend", func(f *fixture) { f.input.SecondIntent.DayTypes = []string{"weekend"} }, 1},
		{"weekday rejects Sunday", func(f *fixture) { f.input.FirstIntent.DayTypes = []string{"weekday"} }, 0},
		{"disjoint day types", func(f *fixture) {
			f.input.FirstIntent.DayTypes = []string{"weekday"}
			f.input.SecondIntent.DayTypes = []string{"weekend"}
		}, 0},
		{"day types OR", func(f *fixture) { f.input.FirstIntent.DayTypes = []string{"weekday", "weekend"} }, 1},
		{"slots OR", func(f *fixture) { f.input.FirstIntent.TimeSlots = []string{"morning", "day"} }, 1},
		{"day AND slot", func(f *fixture) {
			f.input.FirstIntent.DayTypes = []string{"weekend"}
			f.input.FirstIntent.TimeSlots = []string{"night"}
		}, 0},
		{"empty slots", func(f *fixture) { f.input.FirstIntent.TimeSlots = nil; f.input.SecondIntent.TimeSlots = []string{} }, 1},
		{"nightclub excluded by first", func(f *fixture) {
			f.data.Venues[0].VenueType = "nightclub"
			f.input.FirstIntent.ExclusionSlugs = []string{"nightclubs"}
		}, 0},
		{"nightclub without exclusion", func(f *fixture) { f.data.Venues[0].VenueType = "nightclub" }, 1},
		{"very loud excluded by second", func(f *fixture) {
			f.data.Events[0].LoudnessLevel = pgtype.Text{String: "very_loud", Valid: true}
			f.input.SecondIntent.ExclusionSlugs = []string{"very_loud"}
		}, 0},
		{"loud is allowed", func(f *fixture) {
			f.data.Events[0].LoudnessLevel = pgtype.Text{String: "loud", Valid: true}
			f.input.SecondIntent.ExclusionSlugs = []string{"very_loud"}
		}, 1},
		{"outdoor excluded by second", func(f *fixture) {
			f.data.Events[0].Indoor = pgtype.Bool{Bool: false, Valid: true}
			f.input.SecondIntent.ExclusionSlugs = []string{"outdoor"}
		}, 0},
		{"indoor allowed", func(f *fixture) {
			f.data.Events[0].Indoor = pgtype.Bool{Bool: true, Valid: true}
			f.input.SecondIntent.ExclusionSlugs = []string{"outdoor"}
		}, 1},
		{"unknown indoor allowed", func(f *fixture) { f.input.SecondIntent.ExclusionSlugs = []string{"outdoor"} }, 1},
		{"nil radius ignores location", func(f *fixture) { f.input.FirstIntent.Location = &contracts.GeoPoint{Latitude: 70, Longitude: 70} }, 1},
		{"radius uses city center", func(f *fixture) {
			r := int32(1000)
			f.input.FirstIntent.RadiusM = &r
			f.data.City.CenterLat = latitudeForMeters(1000)
		}, 1},
		{"radius rejects just outside center", func(f *fixture) {
			r := int32(1000)
			f.input.FirstIntent.RadiusM = &r
			f.data.City.CenterLat = latitudeForMeters(1000.001)
		}, 0},
		{"location overrides distant center", func(f *fixture) {
			r := int32(1000)
			f.input.FirstIntent.RadiusM = &r
			f.input.FirstIntent.Location = &contracts.GeoPoint{}
			f.data.City.CenterLat = 50
		}, 1},
		{"first radius also binding", func(f *fixture) {
			r := int32(1000)
			f.input.FirstIntent.RadiusM = &r
			f.input.SecondIntent.RadiusM = &r
			f.input.FirstIntent.Location = &contracts.GeoPoint{Latitude: 50}
			f.input.SecondIntent.Location = &contracts.GeoPoint{}
		}, 0},
		{"missing venue", func(f *fixture) { f.data.Venues = nil }, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cityID, venueID := uuid.New(), uuid.New()
			f := fixture{
				input: validInput(cityID),
				data: catalog.Snapshot{
					City:   platform.City{ID: cityID, Timezone: "UTC"},
					Venues: []platform.Venue{{ID: venueID, CityID: cityID}},
					Events: []catalog.Event{event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 1000, true, "published")},
				},
			}
			tc.edit(&f)
			got, err := newBuilder(t, &fakeCatalog{snapshot: f.data}).Build(context.Background(), f.input)
			if err != nil || len(got.Candidates) != tc.want {
				t.Fatalf("candidates=%d want=%d, err=%v", len(got.Candidates), tc.want, err)
			}
		})
	}
}

func TestPoolBuilderMetroUsesNearestValidCityStation(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	in := validInput(cityID)
	in.SecondIntent.ExclusionSlugs = []string{"far_from_metro"}
	snapshot := catalog.Snapshot{
		City:   platform.City{ID: cityID, Timezone: "UTC"},
		Venues: []platform.Venue{{ID: venueID, CityID: cityID}},
		Events: []catalog.Event{event(uuid.New(), venueID, "2026-09-20T13:00:00Z", 0, true, "published")},
	}
	foreign := platform.MetroStation{CityID: uuid.New()}
	invalid := platform.MetroStation{CityID: cityID, Latitude: 360}
	far := platform.MetroStation{CityID: cityID, Latitude: latitudeForMeters(1201)}
	near := platform.MetroStation{CityID: cityID, Latitude: latitudeForMeters(1199)}
	for _, tc := range []struct {
		name     string
		stations []platform.MetroStation
		want     int
	}{
		{"other city", []platform.MetroStation{foreign, far}, 0},
		{"invalid only", []platform.MetroStation{invalid}, 0},
		{"invalid near and valid far", []platform.MetroStation{invalid, far}, 0},
		{"nearest valid station", []platform.MetroStation{far, near}, 1},
		{"nonfinite station", []platform.MetroStation{{CityID: cityID, Latitude: math.NaN()}, far}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot.MetroStations = tc.stations
			got, err := newBuilder(t, &fakeCatalog{snapshot: snapshot}).Build(context.Background(), in)
			if err != nil || len(got.Candidates) != tc.want {
				t.Fatalf("candidates=%d want=%d, err=%v", len(got.Candidates), tc.want, err)
			}
		})
	}
}

func TestPoolBuilderFingerprintCoversNormalizedEffectiveInputs(t *testing.T) {
	cityID, venueID := uuid.New(), uuid.New()
	in := validInput(cityID)
	in.FirstIntent.Dates = []string{"2026-09-20", "2026-09-21"}
	in.FirstIntent.TimeSlots, in.SecondIntent.TimeSlots = nil, nil
	in.PreviousEventIDs = []uuid.UUID{{15: 1}, {15: 2}}
	in.FirstIntent.ExclusionSlugs = []string{"very_loud", "nightclubs"}
	snapshot := catalog.Snapshot{
		City:   platform.City{ID: cityID, Timezone: "UTC"},
		Venues: []platform.Venue{{ID: venueID, CityID: cityID}},
		Events: []catalog.Event{event(uuid.UUID{15: 3}, venueID, "2026-09-20T13:00:00Z", 1000, true, "published")},
	}
	builder := newBuilder(t, &fakeCatalog{snapshot: snapshot})
	before, err := builder.Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	normalized := in
	normalized.FirstIntent.Dates = []string{"2026-09-21", "2026-09-20", "2026-09-20"}
	normalized.FirstIntent.DayTypes = nil
	normalized.SecondIntent.DayTypes = []string{}
	normalized.FirstIntent.TimeSlots = nil
	normalized.FirstIntent.ExclusionSlugs = []string{"nightclubs", "very_loud", "nightclubs"}
	normalized.SecondIntent.ExclusionSlugs = nil
	normalized.FirstIntent.BudgetMaxMinor = 1000
	normalized.PreviousEventIDs = []uuid.UUID{{15: 2}, {15: 1}, {15: 2}}
	after, err := builder.Build(context.Background(), normalized)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("equivalent effective constraints changed result: err=%v before=%+v after=%+v", err, before, after)
	}
	for _, change := range []func(*contracts.BuildInput){
		func(i *contracts.BuildInput) { i.SecondIntent.BudgetMaxMinor-- },
		func(i *contracts.BuildInput) { i.PoolVersion++ },
		func(i *contracts.BuildInput) {
			i.PreviousEventIDs = append(slices.Clone(i.PreviousEventIDs), uuid.UUID{15: 3})
		},
	} {
		changed := in
		change(&changed)
		got, err := builder.Build(context.Background(), changed)
		if err != nil || got.InputFingerprint == before.InputFingerprint {
			t.Fatalf("changed hard input did not change fingerprint: err=%v", err)
		}
	}
	if len(before.InputFingerprint) != 64 {
		t.Fatal("fingerprint is not a SHA-256 digest")
	}
}

func validInput(cityID uuid.UUID) contracts.BuildInput {
	return contracts.BuildInput{RoomID: uuid.New(), CityID: cityID, RoundNo: 1, PoolVersion: 1,
		FirstIntent:  contracts.ParticipantIntent{Dates: []string{"2026-09-20"}, TimeSlots: []string{"day"}, BudgetMaxMinor: 1000},
		SecondIntent: contracts.ParticipantIntent{Dates: []string{"2026-09-20"}, TimeSlots: []string{"day"}, BudgetMaxMinor: 1000}}
}

func event(id, venueID uuid.UUID, starts string, price int32, ticket bool, status string) catalog.Event {
	tm, _ := time.Parse(time.RFC3339, starts)
	return catalog.Event{Event: platform.Event{ID: id, VenueID: venueID, StartsAt: pgtype.Timestamptz{Time: tm, Valid: true}, PriceFromMinor: pgtype.Int4{Int32: price, Valid: true}, TicketAvailable: ticket, TicketUrl: pgtype.Text{String: "https://tickets.example/event", Valid: ticket}, Status: status}}
}

func candidateIDs(candidates []contracts.Candidate) []uuid.UUID {
	ids := make([]uuid.UUID, len(candidates))
	for i := range candidates {
		ids[i] = candidates[i].EventID
	}
	return ids
}

func contains(ids []uuid.UUID, target uuid.UUID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
