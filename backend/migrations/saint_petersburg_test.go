package migrations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
	"github.com/pressly/goose/v3"
)

const saintPetersburgID = "19096037-cba9-55e0-8a99-322ce61554a4"

func TestSaintPetersburgMigrationUpgradeSafety(t *testing.T) {
	fixtureDSN := strings.TrimSpace(os.Getenv("MULTICITY_TEST_DATABASE_URL"))
	if fixtureDSN == "" {
		t.Skip("MULTICITY_TEST_DATABASE_URL is not set")
	}
	fixture, err := testdbguard.Parse(fixtureDSN, "multicity_fixture")
	if err != nil {
		t.Fatal("Saint Petersburg migration requires the approved isolated fixture")
	}

	t.Run("upgrade 32 to 33 adds canonical metadata once", func(t *testing.T) {
		db, pool := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 32)
		assertSaintPetersburgAbsent(t, db)
		upAll(t, db)
		assertSaintPetersburgMetadata(t, db)
		upAll(t, db)
		assertSaintPetersburgMetadata(t, db)
		var applied int
		if err := db.QueryRow(`SELECT count(*) FROM goose_db_version WHERE version_id=33 AND is_applied`).Scan(&applied); err != nil || applied != 1 {
			t.Fatalf("migration 33 applications=%d err=%v, want 1", applied, err)
		}
		assertCurrent(t, pool, true)
	})

	for _, alias := range []string{"САНКТ-ПЕТЕРБУРГ", "СПБ"} {
		t.Run("C locale rejects uppercase Cyrillic alias "+alias, func(t *testing.T) {
			db, _ := newMulticityMigrationDatabase(t, fixture, true)
			upTo(t, db, 32)
			if _, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng)
				VALUES($1,$2,'Europe/Moscow',59.9386,30.3141)`, uuid.New(), alias); err != nil {
				t.Fatal(err)
			}
			mustFailUp33(t, db)
		})
	}

	t.Run("reserved UUID conflicting timezone rolls back", func(t *testing.T) {
		db, _ := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 32)
		if _, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng)
			VALUES($1,'Санкт-Петербург','UTC',59.9386,30.3141)`, saintPetersburgID); err != nil {
			t.Fatal(err)
		}
		mustFailUp33(t, db)
		var timezone string
		if err := db.QueryRow(`SELECT timezone FROM cities WHERE id=$1`, saintPetersburgID).Scan(&timezone); err != nil || timezone != "UTC" {
			t.Fatalf("failed migration changed conflicting timezone=%q err=%v", timezone, err)
		}
	})

	t.Run("Down 33 preserves referenced metadata", func(t *testing.T) {
		db, pool := newMulticityMigrationDatabase(t, fixture, false)
		upAll(t, db)
		userID := uuid.New()
		if _, err := db.Exec(`INSERT INTO users(id,max_user_id,display_name,city_id,onboarding_state)
			VALUES($1,$2,'Saint Petersburg fixture',$3,'complete')`, userID, time.Now().UnixNano(), saintPetersburgID); err != nil {
			t.Fatal(err)
		}
		configureGoose(t)
		if err := goose.DownToContext(context.Background(), db, ".", 32); err != nil {
			t.Fatalf("Down to 32: %v", err)
		}
		assertSaintPetersburgMetadata(t, db)
		var cityID string
		if err := db.QueryRow(`SELECT city_id::text FROM users WHERE id=$1`, userID).Scan(&cityID); err != nil || cityID != saintPetersburgID {
			t.Fatalf("Down 33 changed user city reference=%q err=%v", cityID, err)
		}
		assertCurrent(t, pool, false)
		upAll(t, db)
		assertSaintPetersburgMetadata(t, db)
		assertCurrent(t, pool, true)
	})
}

func mustFailUp33(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := Up(ctx, db); err == nil {
		t.Fatal("migration 33 accepted conflicting Saint Petersburg metadata")
	}
	var applied bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM goose_db_version WHERE version_id=33 AND is_applied)`).Scan(&applied); err != nil || applied {
		t.Fatalf("failed migration 33 recorded as applied=%v err=%v", applied, err)
	}
}

func assertSaintPetersburgAbsent(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM cities WHERE id=$1`, saintPetersburgID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("Saint Petersburg rows before migration=%d err=%v, want 0", count, err)
	}
}

func assertSaintPetersburgMetadata(t *testing.T, db *sql.DB) {
	t.Helper()
	var name, timezone string
	var latitude, longitude float64
	if err := db.QueryRow(`SELECT name,timezone,center_lat,center_lng FROM cities WHERE id=$1`, saintPetersburgID).Scan(&name, &timezone, &latitude, &longitude); err != nil {
		t.Fatal(err)
	}
	if name != "Санкт-Петербург" || timezone != "Europe/Moscow" || latitude != 59.9386 || longitude != 30.3141 {
		t.Fatalf("Saint Petersburg metadata=%q/%q/%v/%v", name, timezone, latitude, longitude)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM cities WHERE id=$1`, saintPetersburgID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Saint Petersburg rows=%d err=%v, want 1", count, err)
	}
}
