package providers

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
)

// These tests intentionally require a separately provisioned, disposable
// database. They never migrate, truncate, or clean it because every fixture
// identity is unique.
func openMulticityIntegrationDB(t *testing.T) *store.Pool {
	t.Helper()
	databaseURL := os.Getenv("MULTICITY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MULTICITY_TEST_DATABASE_URL is not set")
	}
	if _, err := testdbguard.Parse(databaseURL, "multicity_fixture"); err != nil {
		t.Fatal("MULTICITY_TEST_DATABASE_URL must target the approved isolated multicity fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open isolated multicity fixture: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestMulticityReplayAndCrossCityOwnership(t *testing.T) {
	db := openMulticityIntegrationDB(t)
	ctx := context.Background()
	cities := insertMulticityCities(t, db, 2)
	firstCity, secondCity := cities[0], cities[1]
	repo := NewRepository(db)
	source, externalID, venueID := multicityIdentity()
	event := multicityEvent(source, externalID, venueID)

	first, err := repo.UpsertWithResult(ctx, firstCity, event)
	if err != nil || !first.Inserted {
		t.Fatalf("first upsert = %#v, %v", first, err)
	}
	replay, err := repo.UpsertWithResult(ctx, firstCity, event)
	if err != nil || replay.Inserted || replay.EventID != first.EventID {
		t.Fatalf("replay = %#v, %v; want same non-inserted event", replay, err)
	}
	if err := mustRejectUpsert(ctx, repo, secondCity, event); err != nil {
		t.Fatal(err)
	}
	assertMulticityEventCity(t, db, first.EventID, firstCity)
	assertMulticityCounts(t, db, source, externalID, 1, 1)

	// A venue identity cannot silently move when paired with a different event.
	sharedVenue := multicityEvent(source, externalID+"-other", venueID)
	if err := mustRejectUpsert(ctx, repo, secondCity, sharedVenue); err != nil {
		t.Fatal(err)
	}
	assertMulticityCounts(t, db, source, externalID, 1, 1)
	assertMulticityVenueCity(t, db, source, venueID, firstCity)
}

func TestMulticityConcurrentFirstWriteHasOneOwner(t *testing.T) {
	db := openMulticityIntegrationDB(t)
	ctx := context.Background()
	cities := insertMulticityCities(t, db, 2)
	firstCity, secondCity := cities[0], cities[1]
	repo := NewRepository(db)
	source, externalID, venueExternalID := multicityIdentity()
	event := multicityEvent(source, externalID, venueExternalID)

	type outcome struct {
		city, eventID uuid.UUID
		err           error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, city := range []uuid.UUID{firstCity, secondCity} {
		wg.Add(1)
		go func(city uuid.UUID) {
			defer wg.Done()
			<-start
			result, err := repo.UpsertWithResult(ctx, city, event)
			results <- outcome{city: city, eventID: result.EventID, err: err}
		}(city)
	}
	close(start)
	wg.Wait()
	close(results)

	var winner outcome
	successes := 0
	for result := range results {
		if result.err == nil {
			successes++
			winner = result
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent first writes succeeded %d times, want exactly one", successes)
	}
	assertMulticityEventCity(t, db, winner.eventID, winner.city)
	assertMulticityCounts(t, db, source, externalID, 1, 1)
}

func TestMulticityRunScopeAndReconciliation(t *testing.T) {
	db := openMulticityIntegrationDB(t)
	ctx := context.Background()
	cities := insertMulticityCities(t, db, 2)
	cityA, cityB := cities[0], cities[1]
	repo := NewRepository(db)
	source := "multicity-source-" + uuid.NewString()
	otherProvider := "multicity-other-" + uuid.NewString()
	windowStart := time.Now().UTC().Add(-time.Hour)
	windowEnd := windowStart.Add(48 * time.Hour)

	staleA := multicityEvent(source, "stale-a-"+uuid.NewString(), "venue-a-"+uuid.NewString())
	staleAID := mustMulticityUpsert(t, repo, cityA, staleA)
	otherCity := multicityEvent(source, "stale-b-"+uuid.NewString(), "venue-b-"+uuid.NewString())
	otherCityID := mustMulticityUpsert(t, repo, cityB, otherCity)
	otherSource := multicityEvent(otherProvider, "stale-other-"+uuid.NewString(), "venue-other-"+uuid.NewString())
	otherSourceID := mustMulticityUpsert(t, repo, cityA, otherSource)

	runA, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: source, CityID: cityA, WindowStart: windowStart, WindowEnd: windowEnd})
	if err != nil {
		t.Fatal(err)
	}
	wrongCity := multicityEvent(source, "wrong-city-"+uuid.NewString(), "wrong-city-venue-"+uuid.NewString())
	wrongCity.ProviderLastSeenRunID = &runA
	if err := mustRejectUpsert(ctx, repo, cityB, wrongCity); err != nil {
		t.Fatal(err)
	}
	assertMulticityCounts(t, db, source, wrongCity.ExternalID, 0, 0)
	otherRun, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: otherProvider, CityID: cityA, WindowStart: windowStart, WindowEnd: windowEnd})
	if err != nil {
		t.Fatal(err)
	}
	wrongProvider := multicityEvent(source, "wrong-provider-"+uuid.NewString(), "wrong-provider-venue-"+uuid.NewString())
	wrongProvider.ProviderLastSeenRunID = &otherRun
	if err := mustRejectUpsert(ctx, repo, cityA, wrongProvider); err != nil {
		t.Fatal(err)
	}
	assertMulticityCounts(t, db, source, wrongProvider.ExternalID, 0, 0)

	seen := multicityEvent(source, "seen-a-"+uuid.NewString(), "seen-venue-a-"+uuid.NewString())
	seen.ProviderLastSeenRunID = &runA
	seenID := mustMulticityUpsert(t, repo, cityA, seen)
	reconciled, err := repo.FinishSyncRun(ctx, SyncRunFinish{RunID: runA, State: SyncRunSucceeded, Stats: ImportStats{Fetched: 1, Matched: 1, Normalized: 1, Inserted: 1}})
	if err != nil || reconciled != 1 {
		t.Fatalf("finish successful city run = %d, %v; want 1, nil", reconciled, err)
	}
	assertMulticityActive(t, db, staleAID, false)
	assertMulticityActive(t, db, seenID, true)
	assertMulticityActive(t, db, otherCityID, true)
	assertMulticityActive(t, db, otherSourceID, true)
	second, err := repo.FinishSyncRun(ctx, SyncRunFinish{RunID: runA, State: SyncRunSucceeded})
	if err != nil || second != reconciled {
		t.Fatalf("repeat finish = %d, %v; want %d, nil", second, err, reconciled)
	}
	assertMulticityRun(t, db, runA, "succeeded", true, reconciled)

	for _, test := range []struct {
		finish    SyncRunFinish
		wantState string
	}{
		{finish: SyncRunFinish{State: SyncRunFailed}, wantState: "failed"},
		{finish: SyncRunFinish{State: SyncRunCancelled}, wantState: "cancelled"},
		{finish: SyncRunFinish{State: SyncRunSucceeded, Stats: ImportStats{Errors: 1}}, wantState: "failed"},
	} {
		runID, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: source, CityID: cityA, WindowStart: time.Now().UTC(), WindowEnd: time.Now().UTC().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		test.finish.RunID = runID
		if got, err := repo.FinishSyncRun(ctx, test.finish); err != nil || got != 0 {
			t.Fatalf("finish %s = %d, %v", test.finish.State, got, err)
		}
		assertMulticityRun(t, db, runID, test.wantState, true, 0)
		assertMulticityActive(t, db, seenID, true)
	}
	upsertOnly, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: source, CityID: cityA, WindowStart: time.Now().UTC(), WindowEnd: time.Now().UTC().Add(time.Hour), UpsertOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repo.FinishSyncRun(ctx, SyncRunFinish{RunID: upsertOnly, State: SyncRunSucceeded}); err != nil || got != 0 {
		t.Fatalf("finish upsert-only = %d, %v", got, err)
	}
	assertMulticityRun(t, db, upsertOnly, "succeeded", false, 0)
	assertMulticityActive(t, db, seenID, true)
}

func insertMulticityCities(t *testing.T, db *store.Pool, count int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	cities := make([]uuid.UUID, count)
	for i := range cities {
		cities[i] = uuid.New()
		if _, err := db.Exec(ctx, `INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,$2,'Europe/Moscow',55.75,37.61)`, cities[i], "multicity-fixture-"+cities[i].String()); err != nil {
			t.Fatalf("insert fixture city: %v", err)
		}
	}
	return cities
}

func multicityIdentity() (string, string, string) {
	id := uuid.NewString()
	return "multicity-source-" + id, "event-" + id, "venue-" + id
}

func multicityEvent(source, externalID, venueExternalID string) NormalizedEvent {
	return NormalizedEvent{
		Source: source, ExternalID: externalID, Title: "Multicity fixture", Description: "isolated integration fixture",
		Venue:    NormalizedVenue{ExternalID: venueExternalID, Name: "Fixture venue", Address: "Fixture address", VenueType: "other"},
		StartsAt: time.Now().UTC().Add(24 * time.Hour), Timezone: "Europe/Moscow", Currency: "RUB", Status: EventStatusPublished, ProviderActive: true,
		Categories: []NormalizedCategory{{Slug: "other", Weight: 1, IsPrimary: true}},
	}
}

func mustMulticityUpsert(t *testing.T, repo *Repository, cityID uuid.UUID, event NormalizedEvent) uuid.UUID {
	t.Helper()
	result, err := repo.UpsertWithResult(context.Background(), cityID, event)
	if err != nil {
		t.Fatalf("upsert fixture event: %v", err)
	}
	return result.EventID
}

func mustRejectUpsert(ctx context.Context, repo *Repository, cityID uuid.UUID, event NormalizedEvent) error {
	_, err := repo.UpsertWithResult(ctx, cityID, event)
	if err == nil {
		return &multicityExpectedRejection{}
	}
	return nil
}

type multicityExpectedRejection struct{}

func (*multicityExpectedRejection) Error() string {
	return "cross-city or out-of-scope provider upsert was accepted"
}

func assertMulticityCounts(t *testing.T, db *store.Pool, source, externalID string, wantEvents, wantVenues int) {
	t.Helper()
	var events, venues int
	if err := db.QueryRow(context.Background(), `SELECT count(*), (SELECT count(*) FROM venues v WHERE v.id IN (SELECT venue_id FROM events WHERE source=$1 AND external_id=$2)) FROM events WHERE source=$1 AND external_id=$2`, source, externalID).Scan(&events, &venues); err != nil {
		t.Fatalf("read fixture counts: %v", err)
	}
	if events != wantEvents || venues != wantVenues {
		t.Fatalf("fixture rows events/venues=%d/%d, want %d/%d", events, venues, wantEvents, wantVenues)
	}
}

func assertMulticityEventCity(t *testing.T, db *store.Pool, eventID, wantCity uuid.UUID) {
	t.Helper()
	var cityID uuid.UUID
	if err := db.QueryRow(context.Background(), `SELECT v.city_id FROM events e JOIN venues v ON v.id=e.venue_id WHERE e.id=$1`, eventID).Scan(&cityID); err != nil {
		t.Fatalf("read event city: %v", err)
	}
	if cityID != wantCity {
		t.Fatalf("event city=%s, want %s", cityID, wantCity)
	}
}

func assertMulticityVenueCity(t *testing.T, db *store.Pool, source, externalID string, wantCity uuid.UUID) {
	t.Helper()
	venueID := providerStableID("venue", source+"\x00"+externalID)
	var cityID uuid.UUID
	if err := db.QueryRow(context.Background(), `SELECT city_id FROM venues WHERE id=$1`, venueID).Scan(&cityID); err != nil {
		t.Fatalf("read venue city: %v", err)
	}
	if cityID != wantCity {
		t.Fatalf("venue city=%s, want %s", cityID, wantCity)
	}
}

func assertMulticityActive(t *testing.T, db *store.Pool, eventID uuid.UUID, want bool) {
	t.Helper()
	var got bool
	if err := db.QueryRow(context.Background(), `SELECT provider_active FROM events WHERE id=$1`, eventID).Scan(&got); err != nil {
		t.Fatalf("read provider activity: %v", err)
	}
	if got != want {
		t.Fatalf("event %s provider_active=%t, want %t", eventID, got, want)
	}
}

func assertMulticityRun(t *testing.T, db *store.Pool, runID uuid.UUID, wantState string, wantReconcileMissing bool, wantReconciled int) {
	t.Helper()
	var state string
	var reconcileMissing bool
	var reconciled int
	var completed bool
	if err := db.QueryRow(context.Background(), `SELECT state,reconcile_missing,reconciled,completed_at IS NOT NULL FROM provider_sync_runs WHERE id=$1`, runID).Scan(&state, &reconcileMissing, &reconciled, &completed); err != nil {
		t.Fatalf("read sync run: %v", err)
	}
	if state != wantState || reconcileMissing != wantReconcileMissing || reconciled != wantReconciled || !completed {
		t.Fatalf("sync run state/reconcile_missing/reconciled/completed=%q/%t/%d/%t, want %q/%t/%d/true", state, reconcileMissing, reconciled, completed, wantState, wantReconcileMissing, wantReconciled)
	}
}
