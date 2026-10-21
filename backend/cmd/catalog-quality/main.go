// catalog-quality is a read-only catalog diagnostic.  Its JSON output is
// intentionally aggregate-only: it never prints event titles, URLs, DSNs or
// database error messages.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

const revision = "catalog-quality-v1"

const (
	maxFixtureBytes = 16 << 20
	maxFixtureRows  = 10000
)

type options struct {
	fixture, database, city, asOf, revision string
	days                                    int
}
type fixture struct {
	Label    string         `json:"label"`
	City     fixtureCity    `json:"city"`
	Events   []fixtureEvent `json:"events"`
	SyncRuns []fixtureRun   `json:"sync_runs"`
}
type fixtureCity struct {
	ID        string  `json:"id"`
	Timezone  string  `json:"timezone"`
	CenterLat float64 `json:"center_lat"`
	CenterLng float64 `json:"center_lng"`
}
type fixtureEvent struct {
	ID              string         `json:"id"`
	Source          string         `json:"source"`
	ExternalID      string         `json:"external_id"`
	Status          string         `json:"status"`
	StartsAt        string         `json:"starts_at"`
	VenueID         string         `json:"venue_id"`
	Category        string         `json:"category"`
	ProviderActive  bool           `json:"provider_active"`
	TicketAvailable bool           `json:"ticket_available"`
	TicketURL       *string        `json:"ticket_url"`
	Description     *string        `json:"description"`
	PriceFromMinor  *int32         `json:"price_from_minor"`
	Latitude        *float64       `json:"latitude"`
	Longitude       *float64       `json:"longitude"`
	Images          []fixtureImage `json:"images"`
}
type fixtureImage struct {
	Role string `json:"role"`
}
type fixtureRun struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	State       string `json:"state"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	Errors      int    `json:"errors"`
}
type count struct {
	Numerator   int `json:"numerator"`
	Denominator int `json:"denominator"`
}
type sample struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}
type report struct {
	SourceKind    string                  `json:"source_kind"`
	FixtureLabel  string                  `json:"fixture_label,omitempty"`
	Revision      string                  `json:"revision"`
	CityID        string                  `json:"city_id"`
	CityTimezone  string                  `json:"city_timezone"`
	AsOf          string                  `json:"as_of"`
	Days          int                     `json:"days"`
	WindowEnd     string                  `json:"window_end"`
	Limits        map[string]int          `json:"limits"`
	Limitations   []string                `json:"limitations"`
	Sources       map[string]sourceCounts `json:"sources"`
	Completeness  map[string]count        `json:"completeness"`
	Duplicates    map[string][]sample     `json:"duplicates"`
	Samples       []sample                `json:"samples"`
	Sync          map[string]syncSummary  `json:"sync"`
	Scenario      scenarioReport          `json:"scenario"`
	SQLProvenance []string                `json:"sql_provenance,omitempty"`
}
type sourceCounts struct {
	All                            int `json:"all"`
	Future                         int `json:"future"`
	Published                      int `json:"published"`
	ProviderActive                 int `json:"provider_active"`
	TicketAvailable                int `json:"ticket_available"`
	ValidTicketURL                 int `json:"valid_ticket_url"`
	FuturePublishedActiveTicketURL int `json:"future_published_active_ticket_url"`
}
type syncSummary struct {
	Observed    bool   `json:"observed"`
	LastState   string `json:"last_state,omitempty"`
	LastOutcome string `json:"last_outcome,omitempty"`
	LagSeconds  *int64 `json:"lag_seconds,omitempty"`
}
type scenarioReport struct {
	EligibleBeforeRanking int    `json:"eligible_before_ranking"`
	Candidates            int    `json:"candidates"`
	IsSmall               bool   `json:"is_small"`
	CategoryMatched       int    `json:"category_matched"`
	CandidateBucket       string `json:"candidate_bucket"`
	UnknownPriceEligible  int    `json:"unknown_price_eligible"`
	CapReached            bool   `json:"cap_reached"`
}

func main() {
	opt, err := parseOptions(os.Args[1:], time.Now().UTC())
	if err != nil {
		fail(err)
		return
	}
	var out report
	if opt.fixture != "" {
		out, err = runFixture(opt)
	} else {
		out, err = runDatabase(opt)
	}
	if err != nil {
		fail(err)
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
func fail(err error) { fmt.Fprintln(os.Stderr, sanitizeError(err)); os.Exit(2) }
func parseOptions(args []string, now time.Time) (options, error) {
	fs := flag.NewFlagSet("catalog-quality", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var o options
	fs.StringVar(&o.fixture, "fixture", "", "path to offline fixture JSON")
	fs.StringVar(&o.database, "database", "", "expected database name")
	fs.StringVar(&o.city, "city", "", "city UUID")
	fs.StringVar(&o.asOf, "as-of", "", "RFC3339 UTC reference time")
	fs.StringVar(&o.revision, "revision", revision, "catalog revision metadata")
	fs.IntVar(&o.days, "days", 14, "window days (1..90)")
	if err := fs.Parse(args); err != nil {
		return o, errors.New("invalid_arguments")
	}
	if o.city == "" {
		return o, errors.New("invalid_city")
	}
	if _, err := uuid.Parse(o.city); err != nil {
		return o, errors.New("invalid_city")
	}
	if o.days < 1 || o.days > 90 {
		return o, errors.New("invalid_days")
	}
	if o.asOf == "" {
		o.asOf = now.UTC().Format(time.RFC3339)
	} else if v, e := time.Parse(time.RFC3339, o.asOf); e != nil || v.Location() != time.UTC {
		return o, errors.New("invalid_as_of")
	}
	if o.fixture == "" && o.database == "" {
		return o, errors.New("database_required")
	}
	if o.fixture != "" && o.database != "" {
		return o, errors.New("exclusive_source")
	}
	if strings.TrimSpace(o.revision) == "" {
		return o, errors.New("invalid_revision")
	}
	return o, nil
}
func runFixture(o options) (report, error) {
	info, e := os.Stat(o.fixture)
	if e != nil || info.Size() > maxFixtureBytes {
		return report{}, errors.New("fixture_read")
	}
	b, e := os.ReadFile(o.fixture)
	if e != nil {
		return report{}, errors.New("fixture_read")
	}
	var f fixture
	if e = json.Unmarshal(b, &f); e != nil {
		return report{}, errors.New("fixture_parse")
	}
	if strings.TrimSpace(f.Label) == "" {
		return report{}, errors.New("fixture_label_required")
	}
	if f.City.ID != o.city {
		return report{}, errors.New("fixture_city_mismatch")
	}
	if err := validateFixtureIDs(f); err != nil {
		return report{}, err
	}
	return buildReport("fixture", f.Label, o, f)
}
func validateFixtureIDs(f fixture) error {
	if len(f.Events) > maxFixtureRows {
		return errors.New("fixture_rows_limit")
	}
	seen := make(map[uuid.UUID]struct{}, len(f.Events))
	for _, event := range f.Events {
		id, err := uuid.Parse(event.ID)
		if err != nil || id == uuid.Nil {
			return errors.New("fixture_event_id_invalid")
		}
		if _, exists := seen[id]; exists {
			return errors.New("fixture_event_id_duplicate")
		}
		seen[id] = struct{}{}
	}
	return nil
}
func runDatabase(options) (report, error) {
	return report{}, errors.New("database_mode_requires_catalog_readonly_sql")
}
func buildReport(kind, label string, o options, f fixture) (report, error) {
	asof, _ := time.Parse(time.RFC3339, o.asOf)
	zone, e := time.LoadLocation(f.City.Timezone)
	if e != nil {
		return report{}, errors.New("city_timezone_invalid")
	}
	end := asof.AddDate(0, 0, o.days)
	r := report{SourceKind: kind, FixtureLabel: label, Revision: o.revision, CityID: f.City.ID, CityTimezone: f.City.Timezone, AsOf: asof.UTC().Format(time.RFC3339), Days: o.days, WindowEnd: end.UTC().Format(time.RFC3339), Limits: map[string]int{"quality_samples": 20, "duplicate_samples_per_reason": 10, "scenario_candidates": 24}, Sources: map[string]sourceCounts{}, Completeness: map[string]count{}, Duplicates: map[string][]sample{}, Sync: map[string]syncSummary{}, Limitations: []string{"No live URL, image, credential, or external-provider verification.", "Fixture results are offline evidence and are not live database observations.", "An unobserved source is not reported as a zero-success sync."}}
	known := map[string]bool{"concerts": true, "standup": true, "cinema": true, "theatre": true, "exhibitions": true, "sports": true, "food": true, "parties": true, "festivals": true, "walks": true, "other": true}
	dup := map[string][]fixtureEvent{}
	series := map[string][]fixtureEvent{}
	for _, x := range f.Events {
		c := r.Sources[x.Source]
		c.All++
		start, ok := parseTime(x.StartsAt)
		if ok && start.After(asof) && start.Before(end) {
			c.Future++
		}
		if x.Status == "published" {
			c.Published++
		}
		if x.ProviderActive {
			c.ProviderActive++
		}
		if x.TicketAvailable {
			c.TicketAvailable++
		}
		if validURL(x.TicketURL) {
			c.ValidTicketURL++
		}
		if ok && start.After(asof) && start.Before(end) && x.Status == "published" && x.ProviderActive && x.TicketAvailable && validURL(x.TicketURL) {
			c.FuturePublishedActiveTicketURL++
		}
		r.Sources[x.Source] = c
		addMetric(r.Completeness, "card_poster", hasCardPoster(x.Images))
		addMetric(r.Completeness, "valid_coordinates", validCoordinates(x.Latitude, x.Longitude))
		addMetric(r.Completeness, "canonical_category", known[x.Category])
		addMetric(r.Completeness, "known_price", x.PriceFromMinor != nil)
		addMetric(r.Completeness, "valid_time_place", ok && validNonZeroUUID(x.VenueID))
		addMetric(r.Completeness, "description_complete", x.Description != nil && strings.TrimSpace(*x.Description) != "")
		dup[x.Source+"\x00"+x.ExternalID] = append(dup[x.Source+"\x00"+x.ExternalID], x)
		if x.Source == "kudago" {
			if p := strings.Index(x.ExternalID, ":"); p > 0 {
				series[x.Source+"\x00"+x.ExternalID[:p]] = append(series[x.Source+"\x00"+x.ExternalID[:p]], x)
			}
		}
	}
	r.Duplicates["source_external_id"] = nonNilSamples(sampleGroups(dup, "duplicate_source_external_id", 10))
	r.Duplicates["kudago_series_external_id_prefix"] = nonNilSamples(sampleGroups(series, "kudago_series_external_id_prefix", 10))
	r.Duplicates["likely_cross_provider"] = []sample{} // N/D: fixture has no safe cross-provider identity signature.
	r.Samples = nonNilSamples(qualitySamples(f.Events, known, 20))
	r.Sync = syncReport(f.SyncRuns, asof)
	s, e := scenario(f, zone, asof)
	if e != nil {
		return report{}, e
	}
	r.Scenario = s
	return r, nil
}
func addMetric(m map[string]count, k string, good bool) {
	v := m[k]
	v.Denominator++
	if good {
		v.Numerator++
	}
	m[k] = v
}
func parseTime(v string) (time.Time, bool) { t, e := time.Parse(time.RFC3339, v); return t, e == nil }
func validURL(p *string) bool {
	if p == nil || strings.TrimSpace(*p) == "" {
		return false
	}
	u, e := url.ParseRequestURI(*p)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Fragment == ""
}
func hasCardPoster(images []fixtureImage) bool {
	for _, image := range images {
		if image.Role == "card" {
			return true
		}
	}
	return false
}
func validCoordinates(a, b *float64) bool {
	return a != nil && b != nil && *a >= -90 && *a <= 90 && *b >= -180 && *b <= 180
}
func sampleGroups(g map[string][]fixtureEvent, reason string, limit int) []sample {
	var out []sample
	for _, v := range g {
		if len(v) > 1 {
			out = append(out, sample{v[0].ID, reason})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
func nonNilSamples(v []sample) []sample {
	if v == nil {
		return []sample{}
	}
	return v
}
func qualitySamples(v []fixtureEvent, known map[string]bool, limit int) []sample {
	var r []sample
	for _, e := range v {
		reason := ""
		switch {
		case !hasCardPoster(e.Images):
			reason = "missing_card_poster"
		case !validNonZeroUUID(e.VenueID):
			reason = "invalid_venue_id"
		case func() bool { _, ok := parseTime(e.StartsAt); return !ok }():
			reason = "invalid_time_or_place"
		case !validURL(e.TicketURL):
			reason = "invalid_ticket_url"
		case !validCoordinates(e.Latitude, e.Longitude):
			reason = "invalid_coordinates"
		case !known[e.Category]:
			reason = "unknown_category"
		case e.PriceFromMinor == nil:
			reason = "unknown_price"
		case e.Description == nil || strings.TrimSpace(*e.Description) == "":
			reason = "incomplete_description"
		}
		if reason != "" {
			r = append(r, sample{e.ID, reason})
		}
	}
	sort.Slice(r, func(i, j int) bool { return r[i].ID < r[j].ID })
	if len(r) > limit {
		r = r[:limit]
	}
	return r
}
func syncReport(runs []fixtureRun, asof time.Time) map[string]syncSummary {
	m := map[string]syncSummary{}
	for _, run := range runs {
		started, ok := parseTime(run.StartedAt)
		if !ok {
			continue
		}
		old, exists := m[run.Source]
		if exists && old.LagSeconds != nil && started.Before(asof.Add(-time.Duration(*old.LagSeconds)*time.Second)) {
			continue
		}
		s := syncSummary{Observed: true, LastState: run.State}
		switch {
		case run.State == "succeeded" && run.Errors > 0:
			s.LastOutcome = "partial_inferred_errors"
		case run.State == "succeeded":
			s.LastOutcome = "succeeded"
		case run.State == "failed":
			s.LastOutcome = "failed"
		case run.State == "cancelled":
			s.LastOutcome = "cancelled"
		default:
			s.LastOutcome = "running_or_unknown"
		}
		lag := int64(asof.Sub(started).Seconds())
		if lag >= 0 {
			s.LagSeconds = &lag
		}
		m[run.Source] = s
	}
	return m
}

type fixtureCatalog struct{ snapshot catalog.Snapshot }

func (f fixtureCatalog) LoadCity(context.Context, uuid.UUID) (catalog.Snapshot, error) {
	return f.snapshot, nil
}
func scenario(f fixture, zone *time.Location, asof time.Time) (scenarioReport, error) {
	cityID, e := uuid.Parse(f.City.ID)
	if e != nil {
		return scenarioReport{}, errors.New("fixture_city_invalid")
	}
	snap := snapshotFromFixture(f, cityID)
	sat := nextSaturday(asof.In(zone))
	date := sat.Format(time.DateOnly)
	input := contracts.BuildInput{RoomID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), CityID: cityID, PoolVersion: 1, FirstIntent: contracts.ParticipantIntent{UserID: uuid.MustParse("00000000-0000-0000-0000-000000000011"), Dates: []string{date}, TimeSlots: []string{"evening"}, CategorySlugs: []string{"concerts", "standup"}, BudgetMaxMinor: 300000}, SecondIntent: contracts.ParticipantIntent{UserID: uuid.MustParse("00000000-0000-0000-0000-000000000012"), Dates: []string{date}, TimeSlots: []string{"evening"}, CategorySlugs: []string{"concerts", "standup"}, BudgetMaxMinor: 300000}}
	b, e := recommendations.NewPoolBuilderWithClock(fixtureCatalog{snap}, []byte("catalog-quality-tie-break-secret-32b"), func() time.Time { return asof })
	if e != nil {
		return scenarioReport{}, e
	}
	got, e := b.Build(context.Background(), input)
	if e != nil {
		return scenarioReport{}, errors.New("scenario_build")
	}
	eligible := 0
	eligibleIDs := map[uuid.UUID]bool{}
	for _, event := range snap.Events {
		one := snap
		one.Events = []catalog.Event{event}
		x, e := recommendations.NewPoolBuilderWithClock(fixtureCatalog{one}, []byte("catalog-quality-tie-break-secret-32b"), func() time.Time { return asof })
		if e != nil {
			return scenarioReport{}, e
		}
		q, e := x.Build(context.Background(), input)
		if e == nil && len(q.Candidates) > 0 {
			eligible++
			eligibleIDs[event.ID] = true
		}
	}
	candidateIDs := map[uuid.UUID]bool{}
	for _, candidate := range got.Candidates {
		candidateIDs[candidate.EventID] = true
	}
	matched, unknownPriceEligible := 0, 0
	for _, e := range f.Events {
		id, _ := uuid.Parse(e.ID)
		if candidateIDs[id] && (e.Category == "concerts" || e.Category == "standup") {
			matched++
		}
		if e.PriceFromMinor == nil && eligibleIDs[id] {
			unknownPriceEligible++
		}
	}
	return scenarioReport{EligibleBeforeRanking: eligible, Candidates: len(got.Candidates), IsSmall: got.IsSmall, CategoryMatched: matched, CandidateBucket: bucket(len(got.Candidates)), UnknownPriceEligible: unknownPriceEligible, CapReached: len(got.Candidates) == 24}, nil
}
func bucket(n int) string {
	switch {
	case n == 0:
		return "0"
	case n <= 2:
		return "1-2"
	case n <= 9:
		return "3-9"
	default:
		return "10+"
	}
}
func nextSaturday(t time.Time) time.Time {
	d := (int(time.Saturday) - int(t.Weekday()) + 7) % 7
	if d == 0 && t.Hour() >= 22 {
		d = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()+d, 0, 0, 0, 0, t.Location())
}
func snapshotFromFixture(f fixture, city uuid.UUID) catalog.Snapshot {
	snap := catalog.Snapshot{City: platform.City{ID: city, Timezone: f.City.Timezone, CenterLat: f.City.CenterLat, CenterLng: f.City.CenterLng}}
	for _, x := range f.Events {
		if !x.ProviderActive {
			continue // catalog.LoadCity exposes only active-provider records.
		}
		ev, validIdentity := fixtureEventToCatalog(x, city)
		if !validIdentity {
			continue
		}
		snap.Events = append(snap.Events, ev)
		snap.Venues = append(snap.Venues, platform.Venue{ID: ev.VenueID, CityID: city, Latitude: float8(x.Latitude), Longitude: float8(x.Longitude), VenueType: "other"})
	}
	return snap
}
func fixtureEventToCatalog(x fixtureEvent, city uuid.UUID) (catalog.Event, bool) {
	if !validNonZeroUUID(x.ID) || !validNonZeroUUID(x.VenueID) {
		return catalog.Event{}, false
	}
	id, _ := uuid.Parse(x.ID)
	venue, _ := uuid.Parse(x.VenueID)
	start, _ := parseTime(x.StartsAt)
	e := catalog.Event{Event: platform.Event{ID: id, Source: x.Source, ExternalID: x.ExternalID, Status: x.Status, ProviderActive: x.ProviderActive, VenueID: venue, StartsAt: pgtype.Timestamptz{Time: start, Valid: !start.IsZero()}, TicketAvailable: x.TicketAvailable, TicketUrl: text(x.TicketURL), PriceFromMinor: int4(x.PriceFromMinor)}}
	if x.Category != "" {
		e.Categories = []platform.EventCategory{{EventID: id, CategorySlug: x.Category}}
	}
	for _, im := range x.Images {
		e.Images = append(e.Images, platform.EventImage{Role: im.Role})
	}
	return e, true
}
func validNonZeroUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}
func text(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}
func int4(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}
func float8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}
func sanitizeError(e error) string {
	if e == nil {
		return ""
	}
	s := e.Error()
	if strings.Contains(s, "password") || strings.Contains(s, "postgres") || strings.Contains(s, "://") {
		return "operation_failed"
	}
	return s
}
