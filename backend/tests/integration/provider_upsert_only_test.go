package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func TestGenericSyncIsUpsertOnlyAndLocksIdentityAfterPartialImport(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	cityID, sourceID := uuid.New(), uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'Europe/Moscow',55.75,37.61)`, cityID, "generic-sync-"+cityID.String()); err != nil {
		t.Fatal(err)
	}
	sourceKey := "generic:" + uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO event_sources(id,source_key,name,endpoint_url,key_version,default_category,default_timezone,default_currency,default_status,mapping_config)
		VALUES($1,$2,'test','https://example.test/events',1,'other','Europe/Moscow','RUB','published','{"external_id":{"path":"id"}}')`, sourceID, sourceKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM event_sources WHERE id=$1`, sourceID)
		cleanupProviderReconciliationFixture(db, cityID)
	})

	repo := providers.NewRepository(db)
	seen := reconciliationEvent(sourceKey, "seen-"+uuid.NewString(), "seen-venue-"+uuid.NewString())
	unseen := reconciliationEvent(sourceKey, "unseen-"+uuid.NewString(), "unseen-venue-"+uuid.NewString())
	seenID := mustUpsertProviderEvent(t, repo, cityID, seen)
	unseenID := mustUpsertProviderEvent(t, repo, cityID, unseen)
	start := time.Now().UTC()
	runID, err := repo.BeginSyncRun(ctx, providers.SyncRunStart{Provider: sourceKey, CityID: cityID, WindowStart: start, WindowEnd: start.Add(24 * time.Hour), UpsertOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	seen.ProviderLastSeenRunID = &runID
	mustUpsertProviderEvent(t, repo, cityID, seen)
	if _, err := repo.FinishSyncRun(ctx, providers.SyncRunFinish{RunID: runID, State: providers.SyncRunFailed, Stats: providers.ImportStats{Updated: 1, Errors: 1}}); err != nil {
		t.Fatal(err)
	}
	assertProviderActive(t, db, seenID, true)
	assertProviderActive(t, db, unseenID, true)
	var locked, reconcileMissing bool
	if err := db.QueryRow(ctx, `SELECT mapping_locked FROM event_sources WHERE id=$1`, sourceID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT reconcile_missing FROM provider_sync_runs WHERE id=$1`, runID).Scan(&reconcileMissing); err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("external_id mapping was not locked after a persisted event")
	}
	if reconcileMissing {
		t.Fatal("generic run unexpectedly enabled reconciliation")
	}
	if _, err := db.Exec(ctx, `UPDATE event_sources SET mapping_config='{"external_id":{"path":"other_id"}}' WHERE id=$1`, sourceID); err == nil {
		t.Fatal("locked external_id mapping was changed")
	}
}

func TestGenericSyncAdvisoryLockRejectsConcurrentSourceRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	sourceKey := "generic:" + uuid.NewString()
	first, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	var acquired bool
	if err := first.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, sourceKey).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("first source lock = %t, err %v", acquired, err)
	}
	if err := second.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, sourceKey).Scan(&acquired); err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("second concurrent source lock unexpectedly succeeded")
	}
	if err := first.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, sourceKey).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("source lock release = %t, err %v", acquired, err)
	}
	if err := second.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, sourceKey).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("source lock after release = %t, err %v", acquired, err)
	}
	if err := second.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, sourceKey).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("second source lock release = %t, err %v", acquired, err)
	}
}
