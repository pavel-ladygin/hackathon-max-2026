package recommendations

// This evaluator never connects to a database or changes the production ranker.
// The frozen v1 output is replayed against precisely the same fixture inputs.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

const evaluationDir = "testdata/evaluation/"
const evaluationBaselineSHA = "cf58086a73c6cb9d24b57f86ea0f82619f331503"
const evaluationV1 = "scoring-diversity-v6-behavioral"

var evaluationReport = flag.String("evaluation-report", "", "optional Markdown report path; no database access")
var captureEvaluationV1 = flag.Bool("capture-evaluation-v1", false, "one-time capture; refuses to overwrite baseline or capture a different ranker")

type evaluationDataset struct {
	Schema        int
	Source        string
	LabelStatus   string `json:"label_status"`
	HumanReviewed bool   `json:"human_reviewed"`
	Now           time.Time
	Timezone      string
	Cases         []evaluationCase
}

type evaluationIntent struct {
	Categories []string
	Budget     int32
	Dates      []string
	Slots      []string
	Profile    []string
	Behavior   []contracts.BehavioralCategoryCount
	Exclusions []string
}

type evaluationCategory struct {
	Slug   string
	Weight float32
}

type evaluationEvent struct {
	ID         int
	Primary    string
	Secondary  []evaluationCategory
	Fit        [2]float64
	Price      *int32
	Starts     time.Time
	Venue      int
	Eligible   bool // Independently specified oracle, never derived from eligible().
	Status     string
	Ticket     *bool
	Demo       bool
	Source     string
	ExternalID string `json:"external_id"`
}

type evaluationCase struct {
	Name                    string
	Criterion               string
	A, B                    evaluationIntent
	Events                  []evaluationEvent
	RelevantCategories      []string `json:"relevant_categories"`
	ExpectedRepresentatives []int    `json:"expected_representatives"`
	Round                   int16
	Previous                []int
}

type evaluationRow struct {
	Event int
	Score float64
}

type evaluationOutput struct {
	Name, InputSHA256, RankerVersion, Fingerprint string
	IsSmall                                       bool
	Rows                                          []evaluationRow
	PoolDiagnostics                               *contracts.PoolBuildDiagnostics `json:",omitempty"`
}

type evaluationBaseline struct {
	SourceSHA, DatasetSHA256 string
	Cases                    []evaluationOutput
}

type evaluationBehavior map[uuid.UUID][]contracts.BehavioralCategoryCount

type evaluationCatalog struct{ snapshot catalog.Snapshot }

func (c *evaluationCatalog) LoadCity(context.Context, uuid.UUID) (catalog.Snapshot, error) {
	return c.snapshot, nil
}

type evaluationProfiles map[uuid.UUID]contracts.RankingPreferences

func (p evaluationProfiles) LoadRankingPreferences(_ context.Context, id uuid.UUID) (contracts.RankingPreferences, bool, error) {
	value, found := p[id]
	return value, found, nil
}

func (b evaluationBehavior) LoadBehavioralCategoryCounts(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]contracts.BehavioralCategoryCount, error) {
	result := make(map[uuid.UUID][]contracts.BehavioralCategoryCount, len(ids))
	for _, id := range ids {
		result[id] = slices.Clone(b[id])
	}
	return result, nil
}

func evaluationID(kind string, alias int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("synthetic-evaluation/%s/%d", kind, alias)))
}

func evaluationSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func loadEvaluation(t testing.TB) (evaluationDataset, string) {
	t.Helper()
	data, err := os.ReadFile(evaluationDir + "scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	var dataset evaluationDataset
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&dataset); err != nil {
		t.Fatal(err)
	}
	if dataset.Schema != 1 || len(dataset.Cases) < 12 || dataset.Source == "" || dataset.LabelStatus == "" || dataset.Now.IsZero() {
		t.Fatal("incomplete evaluation provenance")
	}
	names := map[string]bool{}
	for _, c := range dataset.Cases {
		if c.Name == "" || names[c.Name] || c.Criterion == "" {
			t.Fatal("case names must be unique and criteria explicit")
		}
		names[c.Name] = true
		events := map[int]evaluationEvent{}
		for _, e := range c.Events {
			if _, duplicate := events[e.ID]; duplicate || e.ID <= 0 || e.Starts.IsZero() || e.Venue < 1 || e.Venue > 4 {
				t.Fatalf("%s: invalid event fixture", c.Name)
			}
			for _, fit := range e.Fit {
				if math.IsNaN(fit) || fit < 0 || fit > 1 {
					t.Fatalf("%s: fit must be explicitly bounded to [0,1]", c.Name)
				}
			}
			events[e.ID] = e
		}
		seen := map[int]bool{}
		for _, id := range c.ExpectedRepresentatives {
			if !events[id].Eligible || seen[id] {
				t.Fatalf("%s: invalid independent representative oracle", c.Name)
			}
			seen[id] = true
		}
	}
	return dataset, evaluationSHA(data)
}

// The input digest includes the materialized catalog, profiles, history, IDs,
// key and clock, so fixture-adapter changes cannot silently invalidate replay.
func newEvaluationBuilder(t testing.TB, dataset evaluationDataset, c evaluationCase) (*PoolBuilder, contracts.BuildInput, map[uuid.UUID]int, string) {
	t.Helper()
	city := evaluationID("city", 1)
	snapshot := catalog.Snapshot{City: platform.City{ID: city, Timezone: dataset.Timezone}}
	for i := 1; i <= 4; i++ {
		venue := platform.Venue{ID: evaluationID("venue", i), CityID: city}
		if i == 4 {
			venue.VenueType = "nightclub"
		}
		snapshot.Venues = append(snapshot.Venues, venue)
	}
	aliases := map[uuid.UUID]int{}
	for _, e := range c.Events {
		id := evaluationID("event", e.ID)
		aliases[id] = e.ID
		status := e.Status
		if status == "" {
			status = "published"
		}
		ticket := e.Ticket == nil || *e.Ticket
		ev := catalog.Event{Event: platform.Event{
			ID: id, VenueID: evaluationID("venue", e.Venue), Status: status,
			StartsAt:        pgtype.Timestamptz{Time: e.Starts, Valid: true},
			TicketAvailable: ticket, TicketUrl: pgtype.Text{String: "https://tickets.invalid/synthetic", Valid: ticket},
			Source: e.Source, ExternalID: e.ExternalID, IsDemo: e.Demo,
		}}
		if e.Price != nil {
			ev.PriceFromMinor = pgtype.Int4{Int32: *e.Price, Valid: true}
		}
		ev.Categories = []platform.EventCategory{{EventID: id, CategorySlug: e.Primary, Weight: 1, IsPrimary: true}}
		for _, cat := range e.Secondary {
			ev.Categories = append(ev.Categories, platform.EventCategory{EventID: id, CategorySlug: cat.Slug, Weight: cat.Weight})
		}
		snapshot.Events = append(snapshot.Events, ev)
	}
	input := contracts.BuildInput{RoomID: evaluationID("room", 1), CityID: city, RoundNo: 1, PoolVersion: 1}
	if c.Round != 0 {
		input.RoundNo, input.PoolVersion = c.Round, int32(c.Round)
	}
	profiles := evaluationProfiles{}
	behavior := evaluationBehavior{}
	for i, intent := range []evaluationIntent{c.A, c.B} {
		id := evaluationID("participant", i+1)
		value := contracts.ParticipantIntent{UserID: id, Version: 1, Dates: intent.Dates, TimeSlots: intent.Slots,
			CategorySlugs: intent.Categories, BudgetMaxMinor: intent.Budget, ExclusionSlugs: intent.Exclusions}
		if i == 0 {
			input.FirstIntent = value
		} else {
			input.SecondIntent = value
		}
		if len(intent.Profile) > 0 {
			profiles[id] = contracts.RankingPreferences{CityID: city, InterestSlugs: intent.Profile, BudgetMaxMinor: intent.Budget, Version: 1}
		}
		behavior[id] = intent.Behavior
	}
	for _, previous := range c.Previous {
		input.PreviousEventIDs = append(input.PreviousEventIDs, evaluationID("event", previous))
	}
	key := []byte("synthetic-evaluation-key-32-bytes!")
	materialized, err := json.Marshal(struct {
		Catalog  catalog.Snapshot
		Input    contracts.BuildInput
		Profiles map[uuid.UUID]contracts.RankingPreferences
		Behavior evaluationBehavior
		Now      time.Time
		Key      []byte
	}{snapshot, input, profiles, behavior, dataset.Now, key})
	if err != nil {
		t.Fatal(err)
	}
	loader := &evaluationCatalog{snapshot: snapshot}
	builder, err := NewPoolBuilderWithClock(loader, key, func() time.Time { return dataset.Now }, profiles)
	if err != nil {
		t.Fatal(err)
	}
	builder.behavior = behavior // Test-only clock + history injection, no production API change.
	return builder, input, aliases, evaluationSHA(materialized)
}

func runEvaluation(t testing.TB, dataset evaluationDataset, c evaluationCase) evaluationOutput {
	t.Helper()
	builder, input, aliases, inputSHA := newEvaluationBuilder(t, dataset, c)
	loader := builder.catalog.(*evaluationCatalog)
	behavior := builder.behavior.(evaluationBehavior)
	before, _ := json.Marshal(struct {
		Catalog  catalog.Snapshot
		Behavior evaluationBehavior
	}{loader.snapshot, behavior})
	result, err := builder.Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	checkPoolDiagnostics(t, result)
	repeated, err := builder.Build(context.Background(), input)
	if err != nil || !reflect.DeepEqual(result, repeated) {
		t.Fatalf("%s: nondeterministic repeated build: %v", c.Name, err)
	}
	after, _ := json.Marshal(struct {
		Catalog  catalog.Snapshot
		Behavior evaluationBehavior
	}{loader.snapshot, behavior})
	if string(before) != string(after) {
		t.Fatalf("%s: evaluator mutated its snapshot", c.Name)
	}
	slices.Reverse(loader.snapshot.Events)
	reversed, err := builder.Build(context.Background(), input)
	if err != nil || !reflect.DeepEqual(result, reversed) {
		t.Fatalf("%s: catalog order changed result: %v", c.Name, err)
	}
	output := evaluationOutput{Name: c.Name, InputSHA256: inputSHA, RankerVersion: result.RankerVersion,
		Fingerprint: result.InputFingerprint, IsSmall: result.IsSmall, Rows: []evaluationRow{}, PoolDiagnostics: result.Diagnostics.Internal}
	for _, candidate := range result.Candidates {
		alias, known := aliases[candidate.EventID]
		if !known {
			t.Fatalf("%s: fabricated event", c.Name)
		}
		output.Rows = append(output.Rows, evaluationRow{Event: alias, Score: float64(candidate.Score.GroupScore)})
	}
	return output
}

type evaluationMetrics struct {
	Score, FitMean, FitMin, CategoryCoverage, Diversity, RelevantRecall *float64
	Unknown, HardViolations, Repeats                                    int
}

func evaluationRatio(numerator, denominator float64) *float64 {
	if denominator == 0 {
		return nil
	}
	value := numerator / denominator
	return &value
}

// Fit is min(labelA,labelB), independent of production scoring and its weights.
// Eligibility and series membership come from explicit, pre-ranking oracles.
func measureEvaluation(c evaluationCase, output evaluationOutput, k int) evaluationMetrics {
	events := map[int]evaluationEvent{}
	relevantTotal := 0
	for _, e := range c.Events {
		events[e.ID] = e
		if slices.Contains(c.ExpectedRepresentatives, e.ID) && min(e.Fit[0], e.Fit[1]) >= .5 {
			relevantTotal++
		}
	}
	rows := output.Rows[:min(k, len(output.Rows))]
	metrics := evaluationMetrics{}
	score, fitSum, fitMinimum := 0.0, 0.0, 1.0
	categories, covered, seen := map[string]bool{}, map[string]bool{}, map[int]bool{}
	relevant := 0
	for _, row := range rows {
		e, known := events[row.Event]
		if !known || !e.Eligible {
			metrics.HardViolations++
		}
		if seen[row.Event] || slices.Contains(c.Previous, row.Event) {
			metrics.Repeats++
		}
		seen[row.Event] = true
		score += row.Score
		fit := min(e.Fit[0], e.Fit[1])
		fitSum += fit
		fitMinimum = min(fitMinimum, fit)
		if fit >= .5 {
			relevant++
		}
		if e.Primary != "" {
			categories[e.Primary] = true
		}
		if slices.Contains(c.RelevantCategories, e.Primary) {
			covered[e.Primary] = true
		}
		for _, secondary := range e.Secondary {
			if secondary.Weight > 0 && slices.Contains(c.RelevantCategories, secondary.Slug) {
				covered[secondary.Slug] = true
			}
		}
		if e.Price == nil {
			metrics.Unknown++
		}
	}
	n := float64(len(rows))
	metrics.Score, metrics.FitMean = evaluationRatio(score, n), evaluationRatio(fitSum, n)
	if n > 0 {
		metrics.FitMin = &fitMinimum
	}
	metrics.Diversity = evaluationRatio(float64(len(categories)), n)
	metrics.CategoryCoverage = evaluationRatio(float64(len(covered)), float64(len(c.RelevantCategories)))
	metrics.RelevantRecall = evaluationRatio(float64(relevant), float64(relevantTotal))
	return metrics
}

func checkEvaluation(t *testing.T, c evaluationCase, output evaluationOutput) {
	t.Helper()
	if len(output.Rows) != min(len(c.ExpectedRepresentatives), 24) || output.IsSmall != (len(output.Rows) == 1 || len(output.Rows) == 2) {
		t.Fatalf("%s: pool size / IsSmall violates independent oracle", c.Name)
	}
	for _, row := range output.Rows {
		if !slices.Contains(c.ExpectedRepresentatives, row.Event) || math.IsNaN(row.Score) || math.IsInf(row.Score, 0) {
			t.Fatalf("%s: invalid representative/score", c.Name)
		}
	}
	metrics := measureEvaluation(c, output, len(output.Rows))
	if metrics.HardViolations != 0 || metrics.Repeats != 0 {
		t.Fatalf("%s: hard violations=%d repeats=%d", c.Name, metrics.HardViolations, metrics.Repeats)
	}
}

func evaluationNumber(value *float64) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%.3f", *value)
}

func renderEvaluation(dataset evaluationDataset, datasetSHA string, baseline evaluationBaseline, current []evaluationOutput) string {
	var report strings.Builder
	fmt.Fprint(&report, "# Локальное сравнение рекомендаций S1\n\n")
	fmt.Fprintf(&report, "Источник: %s.\n\nРазметка: %s. HumanReviewed=%t.\n\n", dataset.Source, dataset.LabelStatus, dataset.HumanReviewed)
	fmt.Fprintf(&report, "Baseline SHA: `%s`. Dataset SHA256: `%s`.\n\n", baseline.SourceSHA, datasetSHA)
	fmt.Fprintf(&report, "Пар: %d. now=`%s`; timezone=`%s`. IDs, clock, key, profiles, history и snapshot одинаковы для обеих версий в каждом кейсе.\n\n", len(dataset.Cases), dataset.Now.Format(time.RFC3339), dataset.Timezone)
	currentLabel := "current (v2)"
	if current[0].RankerVersion == evaluationV1 {
		currentLabel = "current (v2 ещё не реализована)"
	}
	fmt.Fprintf(&report, "v1=`%s`; %s=`%s`. При одинаковой версии результаты обязаны совпадать.\n\n", baseline.Cases[0].RankerVersion, currentLabel, current[0].RankerVersion)
	fmt.Fprint(&report, "Fit — машинные synthetic-ожидания, не независимая человеческая разметка. Этот отчёт не подтверждает улучшение релевантности или реального match rate.\n\n")
	fmt.Fprint(&report, "Средние считаются по фактически выданным min(k,N) событиям; пустое среднее и recall без релевантных кандидатов обозначены —. Score принадлежит модели и не является независимой оценкой.\n\n")
	fmt.Fprintln(&report, "| Кейс | Версия | N | score@5 | score@10 | fit mean@5 | fit min@5 | fit mean@10 | fit min@10 | cat coverage@10 | diversity@10 | relevant recall@5 | relevant recall@10 | unknown@10 | unknown all | hard all | repeats all |")
	fmt.Fprintln(&report, "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	for i, c := range dataset.Cases {
		for v, output := range []evaluationOutput{baseline.Cases[i], current[i]} {
			m5, m10, all := measureEvaluation(c, output, 5), measureEvaluation(c, output, 10), measureEvaluation(c, output, len(output.Rows))
			label := "v1"
			if v == 1 {
				label = "current"
			}
			fmt.Fprintf(&report, "| %s | %s | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %d | %d | %d | %d |\n",
				c.Name, label, len(output.Rows), evaluationNumber(m5.Score), evaluationNumber(m10.Score), evaluationNumber(m5.FitMean), evaluationNumber(m5.FitMin), evaluationNumber(m10.FitMean), evaluationNumber(m10.FitMin), evaluationNumber(m10.CategoryCoverage), evaluationNumber(m10.Diversity), evaluationNumber(m5.RelevantRecall), evaluationNumber(m10.RelevantRecall), m10.Unknown, all.Unknown, all.HardViolations, all.Repeats)
		}
	}
	fmt.Fprint(&report, "\nПорядок top10 (top5 — первые пять), только локальные synthetic aliases:\n\n")
	for i, c := range dataset.Cases {
		fmt.Fprintf(&report, "- `%s`: v1 %v; current %v. %s\n", c.Name, evaluationAliases(baseline.Cases[i].Rows), evaluationAliases(current[i].Rows), c.Criterion)
	}
	fmt.Fprint(&report, "\nВнутренняя диагностика current: последовательные остатки после фильтров; excluded_previous и collapsed_series — число удалённых. final_eligible — до top-k, selected — после. Только evaluator: эти агрегаты не сериализуются в JSON комнаты.\n\n")
	fmt.Fprint(&report, "Классы описывают доступность кандидатов. Nonempty не означает no-match: голоса, общий лайк и финальность третьего раунда определяет lifecycle. Счётчики не доказывают, что смена soft-категории или повышение бюджета только одним участником восстановит пул.\n\n")
	fmt.Fprintln(&report, "| Кейс | Класс | catalog_total | all_future | has_valid_ticket | date_overlap | slot_overlap | budget | venue | excluded_previous | collapsed_series | final_eligible | selected |")
	fmt.Fprintln(&report, "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	for i, c := range dataset.Cases {
		d := current[i].PoolDiagnostics
		if d == nil {
			continue
		} // Historical v1 did not collect private counts.
		fmt.Fprintf(&report, "| %s | %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			c.Name, evaluationPoolAvailability(d), d.CatalogTotal, d.AllFuture, d.HasValidTicket, d.DateOverlap, d.SlotOverlap, d.Budget, d.Venue, d.ExcludedPrevious, d.CollapsedSeries, d.FinalEligible, d.Selected)
	}
	return report.String()
}

func evaluationPoolAvailability(d *contracts.PoolBuildDiagnostics) string {
	switch d.FinalEligible {
	case 0:
		return "zero_eligible"
	case 1, 2:
		return "small_eligible"
	default:
		return "nonempty_eligible"
	}
}

func evaluationAliases(rows []evaluationRow) []int {
	ids := []int{}
	for _, row := range rows[:min(10, len(rows))] {
		ids = append(ids, row.Event)
	}
	return ids
}

func TestOfflineEvaluation(t *testing.T) {
	dataset, datasetSHA := loadEvaluation(t)
	current := make([]evaluationOutput, len(dataset.Cases))
	for i, c := range dataset.Cases {
		t.Run(c.Name, func(t *testing.T) {
			current[i] = runEvaluation(t, dataset, c)
			checkEvaluation(t, c, current[i])
		})
	}
	if t.Failed() {
		return
	}
	path := evaluationDir + "baseline_v1.json"
	if *captureEvaluationV1 {
		for _, output := range current {
			if output.RankerVersion != evaluationV1 {
				t.Fatal("refusing to capture anything other than the original v1")
			}
		}
		data, err := json.MarshalIndent(evaluationBaseline{evaluationBaselineSHA, datasetSHA, current}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatalf("baseline capture is one-time and never overwrites: %v", err)
		}
		_, writeErr := file.Write(append(data, '\n'))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("capture: write=%v close=%v", writeErr, closeErr)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var baseline evaluationBaseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatal(err)
	}
	if baseline.SourceSHA != evaluationBaselineSHA || baseline.DatasetSHA256 != datasetSHA || len(baseline.Cases) != len(current) {
		t.Fatal("baseline provenance changed; compare only identical frozen datasets")
	}
	for i, c := range dataset.Cases {
		old, live := baseline.Cases[i], current[i]
		if old.Name != live.Name || old.InputSHA256 != live.InputSHA256 || old.RankerVersion != evaluationV1 {
			t.Fatalf("%s: baseline inputs / version do not match", c.Name)
		}
		checkEvaluation(t, c, old)
		if old.RankerVersion == live.RankerVersion && !reflect.DeepEqual(old, live) {
			t.Fatalf("%s: ranking changed without a version bump", c.Name)
		}
		if old.RankerVersion != live.RankerVersion && old.Fingerprint == live.Fingerprint {
			t.Fatalf("%s: changed ranker version must change fingerprint", c.Name)
		}
		// These frozen, machine-authored expectations only guard the specified
		// synthetic behavior. They do not establish independent human quality.
		for _, k := range []int{5, 10} {
			oldMetrics, liveMetrics := measureEvaluation(c, old, k), measureEvaluation(c, live, k)
			for name, pair := range map[string][2]*float64{
				"mean fit":    {oldMetrics.FitMean, liveMetrics.FitMean},
				"minimum fit": {oldMetrics.FitMin, liveMetrics.FitMin},
			} {
				if pair[0] != nil && pair[1] != nil && *pair[1]+1e-9 < *pair[0] {
					t.Errorf("%s: synthetic %s@%d regressed: %.3f -> %.3f", c.Name, name, k, *pair[0], *pair[1])
				}
			}
		}
	}
	report := renderEvaluation(dataset, datasetSHA, baseline, current)
	if *evaluationReport != "" {
		if err := os.WriteFile(*evaluationReport, []byte(report), 0644); err != nil {
			t.Fatal(err)
		}
	} else {
		fmt.Print(report)
	}
}

// v1 quality is replayed from the frozen pre-v2 outputs, not timed as though
// replay were a live v1 build. ns/op and allocations describe current Build.
func BenchmarkRankingTopK(b *testing.B) {
	dataset, datasetSHA := loadEvaluation(b)
	data, err := os.ReadFile(evaluationDir + "baseline_v1.json")
	if err != nil {
		b.Fatal(err)
	}
	var baseline evaluationBaseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		b.Fatal(err)
	}
	if baseline.SourceSHA != evaluationBaselineSHA || baseline.DatasetSHA256 != datasetSHA || len(baseline.Cases) != len(dataset.Cases) {
		b.Fatal("baseline provenance mismatch")
	}
	for i, c := range dataset.Cases {
		b.Run(c.Name, func(b *testing.B) {
			builder, input, aliases, inputSHA := newEvaluationBuilder(b, dataset, c)
			old := baseline.Cases[i]
			if old.Name != c.Name || old.InputSHA256 != inputSHA || old.RankerVersion != evaluationV1 {
				b.Fatal("benchmark inputs differ from baseline")
			}
			var result contracts.BuildResult
			b.ReportAllocs()
			for b.Loop() {
				result, err = builder.Build(context.Background(), input)
				if err != nil {
					b.Fatal(err)
				}
			}
			live := evaluationOutput{}
			for _, candidate := range result.Candidates {
				live.Rows = append(live.Rows, evaluationRow{aliases[candidate.EventID], float64(candidate.Score.GroupScore)})
			}
			for _, k := range []int{5, 10} {
				for version, output := range map[string]evaluationOutput{"v1": old, "v2": live} {
					metrics := measureEvaluation(c, output, k)
					for name, value := range map[string]*float64{"fit": metrics.FitMean, "score": metrics.Score, "recall": metrics.RelevantRecall} {
						if value != nil {
							b.ReportMetric(*value, fmt.Sprintf("%s-%s@%d", version, name, k))
						}
					}
				}
			}
		})
	}
}

func TestEvaluationMetricsHandCount(t *testing.T) {
	price := int32(100)
	c := evaluationCase{RelevantCategories: []string{"music"}, ExpectedRepresentatives: []int{1, 2, 3}, Events: []evaluationEvent{
		{ID: 1, Primary: "music", Fit: [2]float64{1, .6}, Eligible: true, Price: &price},
		{ID: 2, Primary: "cinema", Fit: [2]float64{.2, .8}, Eligible: true},
		{ID: 3, Primary: "music", Fit: [2]float64{.9, .9}, Eligible: true, Price: &price},
	}}
	output := evaluationOutput{Rows: []evaluationRow{{1, .8}, {2, .2}}}
	m := measureEvaluation(c, output, 2)
	for name, values := range map[string]struct {
		got  *float64
		want float64
	}{
		"score": {m.Score, .5}, "fit mean": {m.FitMean, .4}, "fit min": {m.FitMin, .2},
		"coverage": {m.CategoryCoverage, 1}, "diversity": {m.Diversity, 1}, "recall": {m.RelevantRecall, .5},
	} {
		if values.got == nil || math.Abs(*values.got-values.want) > 1e-9 {
			t.Errorf("%s: got %v, want %v", name, values.got, values.want)
		}
	}
	if m.Unknown != 1 || m.HardViolations != 0 || m.Repeats != 0 {
		t.Fatalf("incorrect hand-counted safety metrics: %+v", m)
	}
	empty := measureEvaluation(evaluationCase{}, evaluationOutput{}, 10)
	if empty.Score != nil || empty.FitMean != nil || empty.FitMin != nil || empty.Diversity != nil || empty.RelevantRecall != nil || empty.CategoryCoverage != nil {
		t.Fatal("empty denominators must be undefined, not zero or success")
	}
	// A repeated forbidden event must not disappear behind an aggregate average.
	c.Previous = []int{2}
	c.Events[1].Eligible = false
	invalid := measureEvaluation(c, evaluationOutput{Rows: []evaluationRow{{2, .2}, {2, .2}, {999, .9}}}, 10)
	if invalid.HardViolations != 3 || invalid.Repeats != 2 {
		t.Fatalf("violations were hidden: %+v", invalid)
	}
}
