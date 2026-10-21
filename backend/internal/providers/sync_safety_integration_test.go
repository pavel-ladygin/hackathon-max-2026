package providers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

func TestSyncSafetyFailedAndPartialRunsDoNotReconcile(t *testing.T) {
	db := openMulticityIntegrationDB(t)
	ctx := context.Background()
	cityID := insertMulticityCities(t, db, 1)[0]
	repo := NewRepository(db)
	windowStart := time.Now().UTC().Add(-time.Hour)
	windowEnd := windowStart.Add(24 * time.Hour)

	for _, test := range []struct {
		name   string
		finish SyncRunFinish
	}{
		{name: "failed", finish: SyncRunFinish{State: SyncRunFailed, ErrorText: "provider_import_error"}},
		{name: "partial persistence", finish: SyncRunFinish{State: SyncRunSucceeded, Stats: ImportStats{Errors: 1}, ErrorText: "provider_persistence_error"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "kudago"
			stale := multicityEvent(source, "safety-stale-"+uuid.NewString(), "safety-venue-"+uuid.NewString())
			stale.StartsAt = windowStart.Add(time.Hour)
			staleID := mustMulticityUpsert(t, repo, cityID, stale)
			runID, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: source, CityID: cityID, WindowStart: windowStart, WindowEnd: windowEnd})
			if err != nil {
				t.Fatal(err)
			}
			test.finish.RunID = runID
			if reconciled, err := repo.FinishSyncRun(ctx, test.finish); err != nil || reconciled != 0 {
				t.Fatalf("FinishSyncRun() = %d, %v; want 0, nil", reconciled, err)
			}
			assertSyncSafetyActive(t, db, staleID, true)
			assertSyncSafetyRun(t, db, runID, "failed", 0)
		})
	}
}

func TestSyncSafetyUpsertOnlyRunDoesNotReconcile(t *testing.T) {
	db := openMulticityIntegrationDB(t)
	ctx := context.Background()
	cityID := insertMulticityCities(t, db, 1)[0]
	repo := NewRepository(db)
	windowStart := time.Now().UTC().Add(-time.Hour)
	stale := multicityEvent("timepad", "safety-upsert-only-"+uuid.NewString(), "safety-venue-"+uuid.NewString())
	stale.StartsAt = windowStart.Add(time.Hour)
	staleID := mustMulticityUpsert(t, repo, cityID, stale)
	runID, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: "timepad", CityID: cityID, WindowStart: windowStart, WindowEnd: windowStart.Add(24 * time.Hour), UpsertOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled, err := repo.FinishSyncRun(ctx, SyncRunFinish{RunID: runID, State: SyncRunSucceeded}); err != nil || reconciled != 0 {
		t.Fatalf("FinishSyncRun() = %d, %v; want 0, nil", reconciled, err)
	}
	assertSyncSafetyActive(t, db, staleID, true)
	assertSyncSafetyRun(t, db, runID, "succeeded", 0)
}

func TestSyncSafetyFullRunReconcilesOnlyCurrentCityAndWindow(t *testing.T) {
	db := openMulticityIntegrationDB(t)
	ctx := context.Background()
	cities := insertMulticityCities(t, db, 2)
	cityA, cityB := cities[0], cities[1]
	repo := NewRepository(db)
	windowStart := time.Now().UTC().Add(-time.Hour)
	windowEnd := windowStart.Add(24 * time.Hour)
	source := "kudago"

	inside := multicityEvent(source, "safety-inside-"+uuid.NewString(), "safety-venue-"+uuid.NewString())
	inside.StartsAt = windowStart.Add(time.Hour)
	insideID := mustMulticityUpsert(t, repo, cityA, inside)
	outside := multicityEvent(source, "safety-outside-"+uuid.NewString(), "safety-venue-"+uuid.NewString())
	outside.StartsAt = windowEnd.Add(time.Hour)
	outsideID := mustMulticityUpsert(t, repo, cityA, outside)
	otherCity := multicityEvent(source, "safety-other-city-"+uuid.NewString(), "safety-venue-"+uuid.NewString())
	otherCity.StartsAt = windowStart.Add(time.Hour)
	otherCityID := mustMulticityUpsert(t, repo, cityB, otherCity)

	runID, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: source, CityID: cityA, WindowStart: windowStart, WindowEnd: windowEnd})
	if err != nil {
		t.Fatal(err)
	}
	seen := multicityEvent(source, "safety-seen-"+uuid.NewString(), "safety-venue-"+uuid.NewString())
	seen.StartsAt = windowStart.Add(2 * time.Hour)
	seen.ProviderLastSeenRunID = &runID
	seenID := mustMulticityUpsert(t, repo, cityA, seen)
	stats := ImportStats{Fetched: 1, Matched: 1, Normalized: 1, Inserted: 1, InitialTotal: 2, FinalOffset: 1, HTTPAttempts: 1}
	reconciled, err := repo.FinishSyncRun(ctx, SyncRunFinish{RunID: runID, State: SyncRunSucceeded, Stats: stats})
	if err != nil || reconciled != 1 {
		t.Fatalf("FinishSyncRun() = %d, %v; want 1, nil", reconciled, err)
	}
	assertSyncSafetyActive(t, db, insideID, false)
	assertSyncSafetyActive(t, db, seenID, true)
	assertSyncSafetyActive(t, db, outsideID, true)
	assertSyncSafetyActive(t, db, otherCityID, true)
	assertSyncSafetyRun(t, db, runID, "succeeded", 1)
}

func TestSyncSafetyCoverageDropFailsAuditWithoutReconciliation(t *testing.T) {
	db := openMulticityIntegrationDB(t)
	ctx := context.Background()
	cityID := insertMulticityCities(t, db, 1)[0]
	repo := NewRepository(db)
	windowStart := time.Now().UTC().Add(-time.Hour)
	windowEnd := windowStart.Add(24 * time.Hour)
	runID, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: "kudago", CityID: cityID, WindowStart: windowStart, WindowEnd: windowEnd})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uuid.UUID, 0, 10)
	for index := 0; index < 10; index++ {
		event := multicityEvent("kudago", "safety-coverage-"+uuid.NewString(), "safety-venue-"+uuid.NewString())
		event.StartsAt = windowStart.Add(time.Duration(index+1) * time.Hour)
		if index < 4 {
			event.ProviderLastSeenRunID = &runID
		}
		ids = append(ids, mustMulticityUpsert(t, repo, cityID, event))
	}
	_, err = repo.FinishSyncRun(ctx, SyncRunFinish{RunID: runID, State: SyncRunSucceeded, Stats: ImportStats{Fetched: 4, Matched: 4, Normalized: 4, Updated: 4, InitialTotal: 10, FinalOffset: 4}})
	if SyncErrorCode(err) != "provider_coverage_drop" {
		t.Fatalf("FinishSyncRun error code = %q, want provider_coverage_drop (err=%v)", SyncErrorCode(err), err)
	}
	assertSyncSafetyRun(t, db, runID, "failed", 0)
	for _, eventID := range ids {
		assertSyncSafetyActive(t, db, eventID, true)
	}
}

func TestSyncSafetySmallCatalogCoverageDrop(t *testing.T) {
	db := openMulticityIntegrationDB(t)
	ctx := context.Background()
	city := insertMulticityCities(t, db, 1)[0]
	repo := NewRepository(db)
	start := time.Now().UTC()
	run, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: "timepad", CityID: city, WindowStart: start, WindowEnd: start.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for index := 0; index < 9; index++ {
		event := multicityEvent("timepad", "small-coverage-"+uuid.NewString(), "small-venue-"+uuid.NewString())
		event.StartsAt = start.Add(time.Duration(index+1) * time.Hour)
		if index == 0 {
			event.ProviderLastSeenRunID = &run
		}
		ids = append(ids, mustMulticityUpsert(t, repo, city, event))
	}
	_, err = repo.FinishSyncRun(ctx, SyncRunFinish{RunID: run, State: SyncRunSucceeded, Stats: ImportStats{Fetched: 1, Matched: 1, Normalized: 1}})
	if SyncErrorCode(err) != "provider_coverage_drop" {
		t.Fatalf("9 to 1 accepted: %v", err)
	}
	assertSyncSafetyRun(t, db, run, "failed", 0)
	for _, id := range ids {
		assertSyncSafetyActive(t, db, id, true)
	}
}

func assertSyncSafetyActive(t *testing.T, db *store.Pool, eventID uuid.UUID, want bool) {
	t.Helper()
	var active bool
	if err := db.QueryRow(context.Background(), `SELECT provider_active FROM events WHERE id=$1`, eventID).Scan(&active); err != nil {
		t.Fatalf("read event provider activity: %v", err)
	}
	if active != want {
		t.Fatalf("event %s provider_active=%t, want %t", eventID, active, want)
	}
}

func assertSyncSafetyRun(t *testing.T, db *store.Pool, runID uuid.UUID, wantState string, wantReconciled int) {
	t.Helper()
	var state string
	var reconciled int
	var completed bool
	if err := db.QueryRow(context.Background(), `SELECT state, reconciled, completed_at IS NOT NULL FROM provider_sync_runs WHERE id=$1`, runID).Scan(&state, &reconciled, &completed); err != nil {
		t.Fatalf("read provider sync run: %v", err)
	}
	if state != wantState || reconciled != wantReconciled || !completed {
		t.Fatalf("sync run state/reconciled/completed=%q/%d/%t, want %q/%d/true", state, reconciled, completed, wantState, wantReconciled)
	}
}
