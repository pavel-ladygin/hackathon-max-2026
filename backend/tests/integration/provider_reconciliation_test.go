package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

func TestProviderReconciliationLifecycleAndHistoricalReferences(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	cityID, otherCityID := uuid.New(), uuid.New()
	for _, city := range []uuid.UUID{cityID, otherCityID} {
		if _, err := db.Exec(ctx, `INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,$2,'Europe/Moscow',55.75,37.61)`, city, "sync-test-"+city.String()); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { cleanupProviderReconciliationFixture(db, cityID, otherCityID) })

	repo := providers.NewRepository(db)
	seen := reconciliationEvent("kudago", "seen-"+uuid.NewString(), "seen-venue-"+uuid.NewString())
	unseen := reconciliationEvent("kudago", "unseen-"+uuid.NewString(), "unseen-venue-"+uuid.NewString())
	timepadEvent := reconciliationEvent("timepad", "timepad-"+uuid.NewString(), "timepad-venue-"+uuid.NewString())
	otherCityEvent := reconciliationEvent("kudago", "other-city-"+uuid.NewString(), "other-city-venue-"+uuid.NewString())
	seenID := mustUpsertProviderEvent(t, repo, cityID, seen)
	unseenID := mustUpsertProviderEvent(t, repo, cityID, unseen)
	timepadID := mustUpsertProviderEvent(t, repo, cityID, timepadEvent)
	otherCityEventID := mustUpsertProviderEvent(t, repo, otherCityID, otherCityEvent)

	demo := reconciliationEvent("kudago", "demo-"+uuid.NewString(), "demo-venue-"+uuid.NewString())
	demoID := mustUpsertProviderEvent(t, repo, cityID, demo)
	if _, err := db.Exec(ctx, `UPDATE events SET is_demo=true WHERE id=$1`, demoID); err != nil {
		t.Fatal(err)
	}

	createHistoricalReferences(t, db, cityID, unseenID)
	if _, err := db.Exec(ctx, `UPDATE events SET provider_active=false WHERE id=$1`, seenID); err != nil {
		t.Fatal(err)
	}

	windowStart := time.Now().UTC()
	runID, err := repo.BeginSyncRun(ctx, providers.SyncRunStart{Provider: "kudago", CityID: cityID, WindowStart: windowStart, WindowEnd: windowStart.Add(90 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	seen.ProviderLastSeenRunID = &runID
	seenIDAgain := mustUpsertProviderEvent(t, repo, cityID, seen)
	if seenIDAgain != seenID {
		t.Fatalf("returning event ID changed: %s -> %s", seenID, seenIDAgain)
	}
	reconciled, err := repo.FinishSyncRun(ctx, providers.SyncRunFinish{RunID: runID, State: providers.SyncRunSucceeded, Stats: providers.ImportStats{PagesFetched: 2, Fetched: 2, Matched: 2, Normalized: 1, Updated: 1, Skipped: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled != 1 {
		t.Fatalf("reconciled=%d, want 1", reconciled)
	}

	assertProviderActive(t, db, seenID, true)
	assertProviderActive(t, db, unseenID, false)
	assertProviderActive(t, db, timepadID, true)
	assertProviderActive(t, db, otherCityEventID, true)
	assertProviderActive(t, db, demoID, true)

	snapshot, err := catalog.NewRepository(db).LoadCity(ctx, cityID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range snapshot.Events {
		if event.ID == unseenID {
			t.Fatal("inactive provider event remained in active catalog snapshot")
		}
	}
	availability, err := catalog.NewRepository(db).CheckForRoomVote(ctx, unseenID)
	if err != nil {
		t.Fatal(err)
	}
	if !availability.Exists || availability.TicketAvailable {
		t.Fatalf("inactive vote availability = %+v", availability)
	}
	assertHistoricalReferences(t, db, unseenID)

	var state string
	var completed bool
	var storedReconciled, storedPages int
	if err := db.QueryRow(ctx, `SELECT state,completed_at IS NOT NULL,reconciled,pages_fetched FROM provider_sync_runs WHERE id=$1`, runID).Scan(&state, &completed, &storedReconciled, &storedPages); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || !completed || storedReconciled != 1 || storedPages != 2 {
		t.Fatalf("stored run = state %s completed %t reconciled %d pages %d", state, completed, storedReconciled, storedPages)
	}

	returnRun, err := repo.BeginSyncRun(ctx, providers.SyncRunStart{Provider: "kudago", CityID: cityID, WindowStart: windowStart, WindowEnd: windowStart.Add(90 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	unseen.ProviderLastSeenRunID = &returnRun
	mustUpsertProviderEvent(t, repo, cityID, unseen)
	assertProviderActive(t, db, unseenID, true)
	if _, err := repo.FinishSyncRun(ctx, providers.SyncRunFinish{RunID: returnRun, State: providers.SyncRunSucceeded, Stats: providers.ImportStats{Fetched: 1, Matched: 1, Normalized: 1, Updated: 1}}); err != nil {
		t.Fatal(err)
	}
	assertProviderActive(t, db, unseenID, true)
}

func TestProviderReconciliationFailureConcurrencyAndIdempotence(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	cityID := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,$2,'Europe/Moscow',55.75,37.61)`, cityID, "sync-test-"+cityID.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupProviderReconciliationFixture(db, cityID) })
	repo := providers.NewRepository(db)
	event := reconciliationEvent("timepad", "event-"+uuid.NewString(), "venue-"+uuid.NewString())
	eventID := mustUpsertProviderEvent(t, repo, cityID, event)
	now := time.Now().UTC()

	for _, test := range []struct {
		state      providers.SyncRunState
		stats      providers.ImportStats
		wantStored string
	}{
		{state: providers.SyncRunFailed, stats: providers.ImportStats{Errors: 1}, wantStored: "failed"},
		{state: providers.SyncRunCancelled, wantStored: "cancelled"},
		{state: providers.SyncRunSucceeded, stats: providers.ImportStats{Errors: 1}, wantStored: "failed"},
	} {
		runID, err := repo.BeginSyncRun(ctx, providers.SyncRunStart{Provider: "timepad", CityID: cityID, WindowStart: now, WindowEnd: now.Add(90 * 24 * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := repo.FinishSyncRun(ctx, providers.SyncRunFinish{RunID: runID, State: test.state, Stats: test.stats, ErrorText: "provider import did not complete"}); err != nil || got != 0 {
			t.Fatalf("finish %s = %d, %v", test.state, got, err)
		}
		var storedState string
		if err := db.QueryRow(ctx, `SELECT state FROM provider_sync_runs WHERE id=$1`, runID).Scan(&storedState); err != nil || storedState != test.wantStored {
			t.Fatalf("stored state = %q, err %v; want %q", storedState, err, test.wantStored)
		}
		assertProviderActive(t, db, eventID, true)
	}

	oldRun, err := repo.BeginSyncRun(ctx, providers.SyncRunStart{Provider: "timepad", CityID: cityID, WindowStart: now, WindowEnd: now.Add(90 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `SELECT pg_sleep(0.01)`); err != nil {
		t.Fatal(err)
	}
	newRun, err := repo.BeginSyncRun(ctx, providers.SyncRunStart{Provider: "timepad", CityID: cityID, WindowStart: now, WindowEnd: now.Add(90 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	event.ProviderLastSeenRunID = &newRun
	mustUpsertProviderEvent(t, repo, cityID, event)
	if got, err := repo.FinishSyncRun(ctx, providers.SyncRunFinish{RunID: oldRun, State: providers.SyncRunSucceeded}); err != nil || got != 0 {
		t.Fatalf("older concurrent completion = %d, %v", got, err)
	}
	assertProviderActive(t, db, eventID, true)

	firstCount, err := repo.FinishSyncRun(ctx, providers.SyncRunFinish{RunID: newRun, State: providers.SyncRunSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	secondCount, err := repo.FinishSyncRun(ctx, providers.SyncRunFinish{RunID: newRun, State: providers.SyncRunSucceeded})
	if err != nil || secondCount != firstCount {
		t.Fatalf("idempotent completion counts = %d then %d, err %v", firstCount, secondCount, err)
	}
}

func reconciliationEvent(source, externalID, venueExternalID string) providers.NormalizedEvent {
	price := int32(100)
	url := "https://example.test/tickets"
	return providers.NormalizedEvent{
		Source: source, ExternalID: externalID, Title: "Reconciliation event", Description: "fixture",
		Venue:    providers.NormalizedVenue{ExternalID: venueExternalID, Name: "Venue", Address: "Address", VenueType: "other"},
		StartsAt: time.Now().UTC().Add(48 * time.Hour), Timezone: "Europe/Moscow", Currency: "RUB",
		PriceFromMinor: &price, TicketURL: &url, TicketAvailable: true, Status: providers.EventStatusPublished, ProviderActive: true,
		Categories: []providers.NormalizedCategory{{Slug: "other", Weight: 1, IsPrimary: true}},
	}
}

func mustUpsertProviderEvent(t *testing.T, repo *providers.Repository, cityID uuid.UUID, event providers.NormalizedEvent) uuid.UUID {
	t.Helper()
	result, err := repo.UpsertWithResult(context.Background(), cityID, event)
	if err != nil {
		t.Fatal(err)
	}
	return result.EventID
}

func assertProviderActive(t *testing.T, db *store.Pool, eventID uuid.UUID, want bool) {
	t.Helper()
	var got bool
	if err := db.QueryRow(context.Background(), `SELECT provider_active FROM events WHERE id=$1`, eventID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("event %s provider_active=%t, want %t", eventID, got, want)
	}
}

func createHistoricalReferences(t *testing.T, db *store.Pool, cityID, eventID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	userID, roomID, poolID := uuid.New(), uuid.New(), uuid.New()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id,max_user_id,display_name,city_id) VALUES ($1,$2,'reconciliation-user',$3)`, []any{userID, time.Now().UnixNano(), cityID}},
		{`INSERT INTO rooms (id,creator_user_id,city_id,name,state,active_pool_version,expires_at) VALUES ($1,$2,$3,'reconciliation-room','matched',1,now()+interval '1 day')`, []any{roomID, userID, cityID}},
		{`INSERT INTO room_members (room_id,user_id,role) VALUES ($1,$2,'creator')`, []any{roomID, userID}},
		{`INSERT INTO room_pools (id,room_id,version,round_no,ranker_version,input_fingerprint,state,candidate_count,is_small) VALUES ($1,$2,1,1,'test','reconciliation','ready',1,true)`, []any{poolID, roomID}},
		{`INSERT INTO room_pool_events (pool_id,event_id,position,group_score,participant_score_min,participant_score_mean,explanation,feature_snapshot) VALUES ($1,$2,0,1,1,1,'{}','{}')`, []any{poolID, eventID}},
		{`INSERT INTO room_votes (pool_id,room_id,event_id,user_id,vote) VALUES ($1,$2,$3,$4,'like')`, []any{poolID, roomID, eventID, userID}},
		{`INSERT INTO room_matches (id,room_id,pool_id,event_id) VALUES ($1,$2,$3,$4)`, []any{uuid.New(), roomID, poolID, eventID}},
		{`INSERT INTO saved_events (user_id,event_id) VALUES ($1,$2)`, []any{userID, eventID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("create historical reference fixture: %v", err)
		}
	}
}

func assertHistoricalReferences(t *testing.T, db *store.Pool, eventID uuid.UUID) {
	t.Helper()
	var saved, pools, votes, matches int
	if err := db.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM saved_events WHERE event_id=$1),
		(SELECT count(*) FROM room_pool_events WHERE event_id=$1),
		(SELECT count(*) FROM room_votes WHERE event_id=$1),
		(SELECT count(*) FROM room_matches WHERE event_id=$1)`, eventID).Scan(&saved, &pools, &votes, &matches); err != nil {
		t.Fatal(err)
	}
	if saved != 1 || pools != 1 || votes != 1 || matches != 1 {
		t.Fatalf("historical references saved/pool/vote/match = %d/%d/%d/%d", saved, pools, votes, matches)
	}
}

func cleanupProviderReconciliationFixture(db *store.Pool, cityIDs ...uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	queries := []string{
		`DELETE FROM room_matches WHERE room_id IN (SELECT id FROM rooms WHERE city_id = ANY($1::uuid[]))`,
		`DELETE FROM room_votes WHERE room_id IN (SELECT id FROM rooms WHERE city_id = ANY($1::uuid[]))`,
		`DELETE FROM room_pool_events WHERE pool_id IN (SELECT p.id FROM room_pools p JOIN rooms r ON r.id=p.room_id WHERE r.city_id = ANY($1::uuid[]))`,
		`DELETE FROM room_pools WHERE room_id IN (SELECT id FROM rooms WHERE city_id = ANY($1::uuid[]))`,
		`DELETE FROM room_members WHERE room_id IN (SELECT id FROM rooms WHERE city_id = ANY($1::uuid[]))`,
		`DELETE FROM rooms WHERE city_id = ANY($1::uuid[])`,
		`DELETE FROM saved_events WHERE event_id IN (SELECT e.id FROM events e JOIN venues v ON v.id=e.venue_id WHERE v.city_id = ANY($1::uuid[]))`,
		`DELETE FROM users WHERE city_id = ANY($1::uuid[])`,
		`DELETE FROM events WHERE venue_id IN (SELECT id FROM venues WHERE city_id = ANY($1::uuid[]))`,
		`DELETE FROM provider_sync_runs WHERE city_id = ANY($1::uuid[])`,
		`DELETE FROM venues WHERE city_id = ANY($1::uuid[])`,
		`DELETE FROM cities WHERE id = ANY($1::uuid[])`,
	}
	for _, query := range queries {
		_, _ = db.Exec(ctx, query, cityIDs)
	}
}
