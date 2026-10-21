package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
)

const testCity = "10000000-0000-0000-0000-000000000001"

func TestParseOptionsDefaultsUTCAndRejectsBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	o, err := parseOptions([]string{"--fixture", "x.json", "--city", testCity}, now)
	if err != nil || o.asOf != "2026-10-20T10:00:00Z" {
		t.Fatalf("default as-of = %#v, %v", o, err)
	}
	for _, args := range [][]string{
		{"--fixture", "x", "--city", testCity, "--days", "0"},
		{"--fixture", "x", "--city", testCity, "--days", "91"},
		{"--fixture", "x", "--city", testCity, "--as-of", "2026-10-20T13:00:00+03:00"},
	} {
		if _, err := parseOptions(args, now); err == nil {
			t.Fatalf("wanted rejection for %v", args)
		}
	}
}

func TestSanitizeErrorNeverEchoesCredentials(t *testing.T) {
	for _, in := range []error{fmt.Errorf("connect postgres://alice:s3cr3t@example/db"), fmt.Errorf("password authentication failed")} {
		got := sanitizeError(in)
		if got != "operation_failed" || containsAny(got, "alice", "s3cr3t", "example") {
			t.Fatalf("unsafe error %q", got)
		}
	}
}

func TestFixtureReportUnknownPriceIsNotZero(t *testing.T) {
	f := baseFixture()
	f.Events = []fixtureEvent{event("20000000-0000-0000-0000-000000000001", "kudago", "one:1", "2026-10-24T18:00:00Z", nil)}
	r, err := buildReport("fixture", "derived", options{city: testCity, asOf: "2026-10-20T10:00:00Z", days: 14}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Completeness["known_price"]; got.Numerator != 0 || got.Denominator != 1 {
		t.Fatalf("known price = %+v", got)
	}
	if r.Scenario.UnknownPriceEligible != 1 {
		t.Fatalf("unknown eligible price = %+v", r.Scenario)
	}
}

func TestFixtureReportZeroPriceIsKnownAndEligible(t *testing.T) {
	f := baseFixture()
	zero := int32(0)
	f.Events = []fixtureEvent{event("20000000-0000-0000-0000-000000000001", "kudago", "one:1", "2026-10-24T18:00:00Z", &zero)}
	r, err := buildReport("fixture", "derived", options{city: testCity, asOf: "2026-10-20T10:00:00Z", days: 14}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Completeness["known_price"]; got.Numerator != 1 || got.Denominator != 1 {
		t.Fatalf("known price = %+v", got)
	}
	if r.Scenario.EligibleBeforeRanking != 1 {
		t.Fatalf("zero-price event was not eligible: %+v", r.Scenario)
	}
}

func TestScenarioCandidateCounts(t *testing.T) {
	price := int32(100)
	for _, want := range []int{0, 1, 2, 24} {
		t.Run(fmt.Sprintf("%d candidates", want), func(t *testing.T) {
			f := baseFixture()
			for i := 0; i < want; i++ {
				id := fmt.Sprintf("20000000-0000-0000-0000-%012d", i+1)
				f.Events = append(f.Events, event(id, "fixture", fmt.Sprintf("event:%d", i), "2026-10-24T18:00:00Z", &price))
			}
			r, err := scenario(f, time.UTC, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if r.EligibleBeforeRanking != want || r.Candidates != want || r.CapReached != (want == 24) {
				t.Fatalf("scenario=%+v", r)
			}
		})
	}
}

func TestScenarioEligibleCanExceedReturnedCandidatesAndCollapseSeries(t *testing.T) {
	f := baseFixture()
	price := int32(100)
	// All entries meet the Saturday-evening constraints. The first pair shares
	// the KudaGo prefix, so the full builder collapses it while singleton runs
	// still count each event eligible before ranking.
	for i := 0; i < 26; i++ {
		id := fmt.Sprintf("20000000-0000-0000-0000-%012d", i+1)
		ext := fmt.Sprintf("provider-%d:item", i)
		if i < 2 {
			ext = fmt.Sprintf("series:%d", i)
		}
		f.Events = append(f.Events, event(id, "kudago", ext, "2026-10-24T18:00:00Z", &price))
	}
	r, err := scenario(f, time.UTC, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if r.EligibleBeforeRanking != 26 {
		t.Fatalf("eligible=%d", r.EligibleBeforeRanking)
	}
	if r.Candidates > 24 || r.Candidates >= r.EligibleBeforeRanking {
		t.Fatalf("scenario=%+v", r)
	}
	if !r.CapReached {
		t.Fatalf("expected pool capacity indicator: %+v", r)
	}
}

func TestFixtureRejectsMalformedOrOversizedRowSets(t *testing.T) {
	f := baseFixture()
	f.Events = []fixtureEvent{event("00000000-0000-0000-0000-000000000000", "x", "a", "2026-10-24T18:00:00Z", int32ptr(1))}
	if err := validateFixtureIDs(f); err == nil {
		t.Fatal("zero event ID accepted")
	}
	f.Events = []fixtureEvent{event("20000000-0000-0000-0000-000000000001", "x", "a", "2026-10-24T18:00:00Z", int32ptr(1)), event("20000000-0000-0000-0000-000000000001", "x", "b", "2026-10-24T18:00:00Z", int32ptr(1))}
	if err := validateFixtureIDs(f); err == nil {
		t.Fatal("duplicate event ID accepted")
	}
	f.Events = make([]fixtureEvent, maxFixtureRows+1)
	if err := validateFixtureIDs(f); err == nil {
		t.Fatal("row limit accepted")
	}
}

func TestFixtureLabelAndSourceDemarcation(t *testing.T) {
	f := baseFixture()
	price := int32(10)
	f.Events = []fixtureEvent{event("20000000-0000-0000-0000-000000000099", "fixture-source", "fixture:99", "2026-10-24T18:00:00Z", &price)}
	if _, err := buildReport("fixture", "", options{city: testCity, asOf: "2026-10-20T10:00:00Z", days: 1}, f); err != nil {
		t.Fatal(err)
	}
	// The mandatory label is enforced at fixture-file loading; report carries it
	// verbatim only after that check.
	r, err := buildReport("fixture", "derived-corpus", options{city: testCity, asOf: "2026-10-20T10:00:00Z", days: 1}, f)
	if err != nil || r.SourceKind != "fixture" || r.FixtureLabel != "derived-corpus" {
		t.Fatalf("fixture demarcation: %+v, %v", r, err)
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runFixture(options{fixture: p, city: testCity, asOf: "2026-10-20T10:00:00Z", days: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out.Sources["fixture-source"].All != 1 {
		t.Fatalf("fixture JSON fields were not decoded: %#v", out.Sources)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["source_kind"] != "fixture" || wire["city_timezone"] != "UTC" {
		t.Fatalf("bad report JSON: %s", encoded)
	}
	if contains(string(encoded), "example.test") {
		t.Fatalf("report exposed fixture content: %s", encoded)
	}
	f.Label = ""
	b, _ = json.Marshal(f)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runFixture(options{fixture: p, city: testCity, asOf: "2026-10-20T10:00:00Z", days: 1}); err == nil {
		t.Fatal("missing label accepted")
	}
}

func TestNextSaturdayUsesLocalCalendarBoundary(t *testing.T) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	// 22:30 Friday UTC is already Saturday morning in Moscow. Until the local
	// 22:00 cutoff the scenario keeps that Saturday so evening events remain
	// available to the builder.
	got := nextSaturday(time.Date(2026, 10, 23, 22, 30, 0, 0, time.UTC).In(moscow))
	if got.Format(time.DateOnly) != "2026-10-24" {
		t.Fatalf("next Saturday = %s", got)
	}
}

func TestNextSaturdayKeepsUpcomingSaturdayBeforeEveningCutoff(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"saturday morning", time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC), "2026-10-24"},
		{"saturday before cutoff", time.Date(2026, 10, 24, 21, 59, 0, 0, time.UTC), "2026-10-24"},
		{"cutoff", time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC), "2026-10-31"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextSaturday(tc.now).Format(time.DateOnly); got != tc.want {
				t.Fatalf("next Saturday = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestScenarioKeepsRemainingSaturdayEveningEvents(t *testing.T) {
	f := baseFixture()
	price := int32(100)
	f.Events = []fixtureEvent{
		event("20000000-0000-0000-0000-000000000001", "fixture", "remaining:18", "2026-10-24T18:00:00Z", &price),
		event("20000000-0000-0000-0000-000000000002", "fixture", "remaining:21", "2026-10-24T21:00:00Z", &price),
	}
	r, err := scenario(f, time.UTC, time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if r.EligibleBeforeRanking != 2 || r.Candidates != 2 {
		t.Fatalf("remaining Saturday events were not eligible: %+v", r)
	}
}

func TestScenarioRejectsInvalidIdentityOnlyFromEligibility(t *testing.T) {
	f := baseFixture()
	price := int32(100)
	valid := event("20000000-0000-0000-0000-000000000001", "kudago", "valid:1", "2026-10-24T18:00:00Z", &price)
	badVenue := event("20000000-0000-0000-0000-000000000002", "kudago", "bad-venue:1", "2026-10-24T18:00:00Z", &price)
	badVenue.VenueID = "not-a-uuid"
	zeroVenue := event("20000000-0000-0000-0000-000000000003", "kudago", "zero-venue:1", "2026-10-24T18:00:00Z", &price)
	zeroVenue.VenueID = uuid.Nil.String()
	badDate := event("20000000-0000-0000-0000-000000000004", "kudago", "bad-date:1", "not-a-time", &price)
	f.Events = []fixtureEvent{valid, badVenue, zeroVenue, badDate}

	r, err := buildReport("fixture", "derived", options{city: testCity, asOf: "2026-10-20T10:00:00Z", days: 14}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Sources["kudago"].All; got != 4 {
		t.Fatalf("raw source count = %d, want 4", got)
	}
	if got := r.Completeness["known_price"]; got.Denominator != 4 {
		t.Fatalf("dataset denominator = %+v, want 4", got)
	}
	if got := r.Completeness["valid_time_place"]; got.Numerator != 1 || got.Denominator != 4 {
		t.Fatalf("valid time/place = %+v, want 1/4", got)
	}
	reasons := map[string]string{}
	for _, sample := range r.Samples {
		reasons[sample.ID] = sample.Reason
	}
	if reasons[badVenue.ID] != "invalid_venue_id" || reasons[zeroVenue.ID] != "invalid_venue_id" {
		t.Fatalf("venue audit samples = %+v", r.Samples)
	}
	if r.Scenario.EligibleBeforeRanking != 1 || r.Scenario.Candidates != 1 {
		t.Fatalf("invalid identity/date leaked into eligibility: %+v", r.Scenario)
	}
}

func baseFixture() fixture {
	return fixture{Label: "derived", City: fixtureCity{ID: testCity, Timezone: "UTC", CenterLat: 55.7, CenterLng: 37.6}}
}
func event(id, source, external, start string, price *int32) fixtureEvent {
	u := "https://example.test/ticket"
	d := "description"
	lat, lng := 55.7, 37.6
	return fixtureEvent{ID: id, Source: source, ExternalID: external, Status: "published", StartsAt: start, VenueID: uuid.New().String(), Category: "concerts", ProviderActive: true, TicketAvailable: true, TicketURL: &u, Description: &d, PriceFromMinor: price, Latitude: &lat, Longitude: &lng, Images: []fixtureImage{{Role: "card"}}}
}
func int32ptr(v int32) *int32 { return &v }
func containsAny(s string, values ...string) bool {
	for _, v := range values {
		if v != "" && contains(s, v) {
			return true
		}
	}
	return false
}
func contains(s, part string) bool {
	return len(part) <= len(s) && (s == part || (len(part) > 0 && index(s, part) >= 0))
}
func index(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}

// Keep this compile-time assertion close to the scenario test: it documents
// that the command uses the public builder contract, without profile loaders.
var _ contracts.PoolBuilder = (*recommendations.PoolBuilder)(nil)
var _ = catalog.Snapshot{}
var _ = context.Background
