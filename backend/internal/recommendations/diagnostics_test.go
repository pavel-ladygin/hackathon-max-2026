package recommendations

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
)

func checkPoolDiagnostics(t testing.TB, result contracts.BuildResult) {
	t.Helper()
	d := result.Diagnostics.Internal
	if d == nil {
		t.Fatal("builder omitted internal diagnostics")
	}
	stages := []int{d.CatalogTotal, d.AllFuture, d.HasValidTicket, d.DateOverlap, d.SlotOverlap, d.Budget, d.Venue}
	for i, count := range stages {
		if count < 0 || (i > 0 && count > stages[i-1]) {
			t.Fatal("survivor counts must be nonnegative and monotonic")
		}
	}
	if d.ExcludedPrevious < 0 || d.CollapsedSeries < 0 || d.FinalEligible < 0 ||
		d.Venue-d.ExcludedPrevious-d.CollapsedSeries != d.FinalEligible ||
		d.Selected != min(d.FinalEligible, poolMax) || d.Selected != len(result.Candidates) {
		t.Fatalf("candidate conservation failed: %+v", d)
	}
	if result.IsSmall != (d.Selected == 1 || d.Selected == 2) {
		t.Fatal("small flag disagrees with candidate count")
	}
	if d.Selected == 0 {
		if len(result.Diagnostics.Reasons) != 1 || result.Diagnostics.Reasons[0].Code != "catalog_shortage" {
			t.Fatal("empty pool must retain the existing generic public reason")
		}
	} else if len(result.Diagnostics.Reasons) != 0 {
		t.Fatal("candidate availability is not evidence of no-match")
	}
}

func diagnosticCounts(d *contracts.PoolBuildDiagnostics) [11]int {
	return [11]int{d.CatalogTotal, d.AllFuture, d.HasValidTicket, d.DateOverlap, d.SlotOverlap, d.Budget, d.Venue,
		d.ExcludedPrevious, d.CollapsedSeries, d.FinalEligible, d.Selected}
}

func TestPoolDiagnosticsHandCountedScenarios(t *testing.T) {
	dataset, _ := loadEvaluation(t)
	// Tuple order: catalog, future, ticket, date, slot, budget, venue,
	// excluded previous, collapsed series, final eligible, selected.
	expected := map[string][11]int{
		"zero_date_overlap":   {4, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0},
		"zero_slot_overlap":   {4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 0},
		"soft_category_only":  {4, 4, 4, 4, 4, 4, 4, 0, 0, 4, 4},
		"unknown_price":       {3, 3, 3, 3, 3, 3, 3, 0, 0, 3, 3},
		"all_unknown_price":   {6, 6, 6, 6, 6, 6, 6, 0, 0, 6, 6},
		"asymmetric_budget":   {4, 4, 4, 4, 4, 3, 3, 0, 0, 3, 3},
		"raised_other_budget": {4, 4, 4, 4, 4, 3, 3, 0, 0, 3, 3},
		"pool_0":              {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"pool_1":              {1, 1, 1, 1, 1, 1, 1, 0, 0, 1, 1},
		"pool_2":              {2, 2, 2, 2, 2, 2, 2, 0, 0, 2, 2},
		"pool_24":             {24, 24, 24, 24, 24, 24, 24, 0, 0, 24, 24},
		"pool_30":             {30, 30, 30, 30, 30, 30, 30, 0, 0, 30, 24},
		"repeat_round_2":      {5, 5, 5, 5, 5, 5, 5, 2, 0, 3, 3},
		"repeat_round_3":      {5, 5, 5, 5, 5, 5, 5, 4, 0, 1, 1},
		"same_series":         {2, 2, 2, 2, 2, 2, 2, 0, 1, 1, 1},
		"expired_time":        {3, 1, 1, 1, 1, 1, 1, 0, 0, 1, 1},
		"ticket_status":       {4, 4, 1, 1, 1, 1, 1, 0, 0, 1, 1},
		"venue_exclusion":     {2, 2, 2, 2, 2, 2, 1, 0, 0, 1, 1},
	}
	tested := 0
	for _, c := range dataset.Cases {
		want, exists := expected[c.Name]
		if !exists {
			continue
		}
		tested++
		t.Run(c.Name, func(t *testing.T) {
			output := runEvaluation(t, dataset, c)
			if got := diagnosticCounts(output.PoolDiagnostics); got != want {
				t.Fatalf("counts=%v, hand-counted=%v", got, want)
			}
		})
	}
	if tested != len(expected) {
		t.Fatal("a required diagnostics fixture was not executed")
	}
}

func TestPoolDiagnosticsSequentialAttribution(t *testing.T) {
	dataset, _ := loadEvaluation(t)
	intent := evaluationIntent{Categories: []string{"music"}, Budget: 100000, Dates: []string{"2026-10-24"}, Slots: []string{"day"}, Exclusions: []string{"nightclubs"}}
	c := evaluationCase{Name: "one_removal_per_stage", A: intent, B: intent}
	price := int32(10000)
	for i := 1; i <= 10; i++ {
		c.Events = append(c.Events, evaluationEvent{ID: i, Primary: "music", Price: &price, Venue: (i-1)%3 + 1,
			Starts: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC), Eligible: true})
	}
	c.Events[0].Starts = dataset.Now
	noTicket := false
	c.Events[1].Ticket = &noTicket
	c.Events[2].Starts = c.Events[2].Starts.AddDate(0, 0, 1)
	c.Events[3].Starts = time.Date(2026, 10, 24, 16, 0, 0, 0, time.UTC) // 19:00 in city, outside requested day slot.
	expensive := int32(100001)
	c.Events[4].Price = &expensive
	c.Events[5].Venue = 4
	c.Previous = []int{7}
	c.Events[7].Source, c.Events[7].ExternalID = "synthetic", "verified-series"
	c.Events[8].Source, c.Events[8].ExternalID = "synthetic", "verified-series"
	c.Events[8].Starts = c.Events[8].Starts.Add(time.Hour)
	c.Events[9].Price = nil
	for i := range c.Events {
		c.Events[i].Eligible = i >= 7
	}
	c.ExpectedRepresentatives = []int{8, 10}
	got := runEvaluation(t, dataset, c)
	want := [11]int{10, 9, 8, 7, 6, 5, 4, 1, 1, 2, 2}
	if diagnosticCounts(got.PoolDiagnostics) != want {
		t.Fatalf("counts=%v, want %v", diagnosticCounts(got.PoolDiagnostics), want)
	}
	// A previous ID already rejected by a hard filter is not removed twice.
	c.Previous = append(c.Previous, 1, 2, 3, 4, 5, 6)
	again := runEvaluation(t, dataset, c)
	if !reflect.DeepEqual(got.PoolDiagnostics, again.PoolDiagnostics) {
		t.Fatal("previous IDs double-counted rejected candidates")
	}
}

func TestPoolAvailabilityDoesNotInferRoundOrVoteOutcome(t *testing.T) {
	dataset, _ := loadEvaluation(t)
	var fixture evaluationCase
	for _, c := range dataset.Cases {
		if c.Name == "pool_2" {
			fixture = c
		}
	}
	for _, round := range []int16{1, 3} {
		fixture.Round = round
		got := runEvaluation(t, dataset, fixture)
		if evaluationPoolAvailability(got.PoolDiagnostics) != "small_eligible" {
			t.Fatal("round number substituted for candidate availability")
		}
	}
	fixture.Previous = []int{1, 2}
	for i := range fixture.Events {
		fixture.Events[i].Eligible = false
	}
	fixture.ExpectedRepresentatives = nil
	got := runEvaluation(t, dataset, fixture)
	if evaluationPoolAvailability(got.PoolDiagnostics) != "zero_eligible" || got.PoolDiagnostics.ExcludedPrevious != 2 {
		t.Fatal("third round with all previous events must be zero eligible, not a ranker no-match conclusion")
	}
	// All unavailable tickets is a zero-pool condition. "All disliked" is a
	// different lifecycle outcome: neither BuildInput nor these counters has
	// current-round votes, so the builder cannot classify it.
	fixture.Previous = nil
	noTicket := false
	for i := range fixture.Events {
		fixture.Events[i].Ticket = &noTicket
	}
	got = runEvaluation(t, dataset, fixture)
	if got.PoolDiagnostics.HasValidTicket != 0 || got.PoolDiagnostics.ExcludedPrevious != 0 {
		t.Fatal("all rejected by eligibility misclassified")
	}
}

func TestPoolDiagnosticsDoNotOfferCategoryOrUnilateralBudgetRecovery(t *testing.T) {
	dataset, _ := loadEvaluation(t)
	var fixture evaluationCase
	for _, c := range dataset.Cases {
		if c.Name == "asymmetric_budget" {
			fixture = c
		}
	}
	fixture.B.Budget = 7777777
	initial := runEvaluation(t, dataset, fixture)
	fixture.B.Budget = 8888888
	fixture.A.Categories, fixture.B.Categories = []string{"sports"}, []string{"theatre"}
	changed := runEvaluation(t, dataset, fixture)
	if !reflect.DeepEqual(initial.PoolDiagnostics, changed.PoolDiagnostics) {
		t.Fatal("soft category or higher non-binding budget changed eligibility counts")
	}
	// Force zero overlap and inject private sentinels into every private field
	// that a diagnostics shortcut might otherwise copy into a suggestion.
	builder, input, _, _ := newEvaluationBuilder(t, dataset, fixture)
	input.FirstIntent.Dates, input.SecondIntent.Dates = []string{"2026-10-24"}, []string{"2026-10-25"}
	input.FirstIntent.UserID, input.SecondIntent.UserID = uuid.New(), uuid.New()
	secret := "private-preference-sentinel"
	input.SecondIntent.FreeText = &secret
	result, err := builder.Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	checkPoolDiagnostics(t, result)
	public, err := json.Marshal(result.Diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"reasons":[{"code":"catalog_shortage","text":"Недостаточно подходящих событий в каталоге."}]}`
	if string(public) != want {
		t.Fatalf("public diagnostics contract changed: %s", public)
	}
	private, err := json.Marshal(result.Diagnostics.Internal)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, input.FirstIntent.UserID.String(), input.SecondIntent.UserID.String(), "2026-10-24", "2026-10-25", "8888888", "sports", "theatre", "no_match", "exhausted"} {
		if strings.Contains(string(public), forbidden) || strings.Contains(string(private), forbidden) {
			t.Fatalf("diagnostics exposed private data or inferred lifecycle: %q", forbidden)
		}
	}
	// Invalid start and missing venue must be attributed to their own stages.
	snapshot := builder.catalog.(*evaluationCatalog)
	snapshot.snapshot.Events = snapshot.snapshot.Events[:1]
	snapshot.snapshot.Events[0].StartsAt = pgtype.Timestamptz{}
	invalid, err := builder.Build(context.Background(), input)
	if err != nil || invalid.Diagnostics.Internal.AllFuture != 0 {
		t.Fatal("invalid start counted as future")
	}
	input.SecondIntent.Dates = input.FirstIntent.Dates
	snapshot.snapshot.Events[0].StartsAt = pgtype.Timestamptz{Time: fixture.Events[0].Starts, Valid: true}
	snapshot.snapshot.Events[0].VenueID = uuid.New()
	missing, err := builder.Build(context.Background(), input)
	if err != nil || missing.Diagnostics.Internal.Budget != 1 || missing.Diagnostics.Internal.Venue != 0 {
		t.Fatal("missing city venue misclassified")
	}
}
