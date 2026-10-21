package migrations

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
	"github.com/pressly/goose/v3"
)

func TestRecommendationAnalyticsMigrationUpgrade(t *testing.T) {
	dsn := os.Getenv("MULTICITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MULTICITY_TEST_DATABASE_URL is not set")
	}
	fixture, err := testdbguard.Parse(dsn, "multicity_fixture")
	if err != nil {
		t.Fatal("analytics migration tests require the approved isolated fixture")
	}
	assertViews := func(t *testing.T, db *sql.DB, want bool) {
		t.Helper()
		for _, name := range []string{"analytics_recommendation_rounds", "analytics_recommendation_room_events", "analytics_recommendation_rooms"} {
			var present bool
			if err := db.QueryRow("SELECT to_regclass($1) IS NOT NULL", name).Scan(&present); err != nil || present != want {
				t.Fatalf("view %s present=%t, want %t: %v", name, present, want, err)
			}
		}
	}
	t.Run("fresh database", func(t *testing.T) {
		db, pool := newMulticityMigrationDatabase(t, fixture, false)
		upAll(t, db)
		assertCurrent(t, pool, true)
		assertViews(t, db, true)
	})
	t.Run("upgrade from released 31 preserves data and legacy views", func(t *testing.T) {
		db, pool := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 31)
		assertViews(t, db, false)
		assertCurrent(t, pool, false)
		refs := seedVersion30References(t, db)
		var legacyBefore string
		if err := db.QueryRow("SELECT pg_get_viewdef('analytics_room_funnel_metrics'::regclass)").Scan(&legacyBefore); err != nil {
			t.Fatal(err)
		}
		upAll(t, db)
		upAll(t, db) // A repeated deployment must not re-create the views.
		assertCurrent(t, pool, true)
		assertViews(t, db, true)
		assertVersion30References(t, db, refs)
		var applied32, recorded27 int
		if err := db.QueryRow(`SELECT count(*) FILTER (WHERE version_id=32 AND is_applied),
		 count(*) FILTER (WHERE version_id=27) FROM goose_db_version`).Scan(&applied32, &recorded27); err != nil || applied32 != 1 || recorded27 != 0 {
			t.Fatalf("upgrade must apply 32 once without backfilling 27: applied32=%d recorded27=%d err=%v", applied32, recorded27, err)
		}
		var rooms, rounds int
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM analytics_recommendation_rooms),
		 (SELECT count(*) FROM analytics_recommendation_rounds)`).Scan(&rooms, &rounds); err != nil || rooms != 1 || rounds != 1 {
			t.Fatalf("upgraded analytics lost historical fixtures: rooms=%d rounds=%d err=%v", rooms, rounds, err)
		}
		if err := goose.DownToContext(context.Background(), db, ".", 31); err != nil {
			t.Fatalf("rollback analytics to released 31: %v", err)
		}
		assertViews(t, db, false)
		assertVersion30References(t, db, refs)
		var legacyAfter string
		if err := db.QueryRow("SELECT pg_get_viewdef('analytics_room_funnel_metrics'::regclass)").Scan(&legacyAfter); err != nil || legacyBefore != legacyAfter {
			t.Fatalf("analytics migration changed a legacy view: %v", err)
		}
		upAll(t, db)
		assertCurrent(t, pool, true)
		assertViews(t, db, true)
	})
}
