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
	t.Run("upgrade from released 32 preserves uploads, data, and legacy views", func(t *testing.T) {
		db, pool := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 32)
		assertViews(t, db, false)
		assertCurrent(t, pool, false)
		refs := seedVersion30References(t, db)
		upload := seedVersion32ImageUpload(t, db, refs)
		var legacyBefore string
		if err := db.QueryRow("SELECT pg_get_viewdef('analytics_room_funnel_metrics'::regclass)").Scan(&legacyBefore); err != nil {
			t.Fatal(err)
		}
		upAll(t, db)
		upAll(t, db) // A repeated deployment must not re-create the views.
		assertCurrent(t, pool, true)
		assertViews(t, db, true)
		assertVersion30References(t, db, refs)
		assertVersion32Upload(t, db, upload)
		assertUpgrade32Applied33Then34(t, db)
		var applied32, applied33, applied34, recorded27 int
		if err := db.QueryRow(`SELECT count(*) FILTER (WHERE version_id=32 AND is_applied),
		 count(*) FILTER (WHERE version_id=33 AND is_applied),
		 count(*) FILTER (WHERE version_id=34 AND is_applied),
		 count(*) FILTER (WHERE version_id=27) FROM goose_db_version`).Scan(&applied32, &applied33, &applied34, &recorded27); err != nil || applied32 != 1 || applied33 != 1 || applied34 != 1 || recorded27 != 0 {
			t.Fatalf("upgrade must retain 32 and apply 33/34 once without backfilling 27: 32=%d 33=%d 34=%d 27=%d err=%v", applied32, applied33, applied34, recorded27, err)
		}
		var rooms, rounds int
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM analytics_recommendation_rooms),
		 (SELECT count(*) FROM analytics_recommendation_rounds)`).Scan(&rooms, &rounds); err != nil || rooms != 1 || rounds != 1 {
			t.Fatalf("upgraded analytics lost historical fixtures: rooms=%d rounds=%d err=%v", rooms, rounds, err)
		}
		if err := goose.DownToContext(context.Background(), db, ".", 32); err != nil {
			t.Fatalf("rollback analytics to released 32: %v", err)
		}
		assertViews(t, db, false)
		assertVersion30References(t, db, refs)
		assertVersion32Upload(t, db, upload)
		var legacyAfter string
		if err := db.QueryRow("SELECT pg_get_viewdef('analytics_room_funnel_metrics'::regclass)").Scan(&legacyAfter); err != nil || legacyBefore != legacyAfter {
			t.Fatalf("analytics migration changed a legacy view: %v", err)
		}
		upAll(t, db)
		assertCurrent(t, pool, true)
		assertViews(t, db, true)
		assertVersion32Upload(t, db, upload)
	})
}

func assertUpgrade32Applied33Then34(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT version_id FROM goose_db_version
		WHERE id > (SELECT max(id) FROM goose_db_version WHERE version_id=32 AND is_applied)
		  AND is_applied
		ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0] != 33 || versions[1] != 34 {
		t.Fatalf("versions applied after released 32 = %v, want [33 34]", versions)
	}
}
