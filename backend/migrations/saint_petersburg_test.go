package migrations

import (
	"os"
	"testing"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
	"github.com/pressly/goose/v3"
)

func TestSaintPetersburgMigration34(t *testing.T) {
	dsn := os.Getenv("MULTICITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated local migration fixture is not configured")
	}
	fixture, err := testdbguard.Parse(dsn, "multicity_fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []string{"", "other UUID", "reserved UUID"} {
		t.Run(conflict, func(t *testing.T) {
			db, _ := newMulticityMigrationDatabase(t, fixture, true)
			upTo(t, db, 33)
			if conflict == "other UUID" {
				_, err = db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES ('e9853e3a-7e7b-47ab-a69f-4f184fb3df46','САНКТ-ПЕТЕРБУРГ','Europe/Moscow',59.939095,30.315868)`)
			}
			if conflict == "reserved UUID" {
				_, err = db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES ('bf61fbf5-8c5b-53da-96c7-3560df3f8177','Other','UTC',59.939095,30.315868)`)
			}
			if err != nil {
				t.Fatal(err)
			}
			configureGoose(t)
			upErr := goose.UpTo(db, ".", 34)
			if conflict != "" {
				if upErr == nil {
					t.Fatal("migration accepted a conflicting city identity")
				}
				return
			}
			if upErr != nil {
				t.Fatal(upErr)
			}
			if err := goose.UpTo(db, ".", 34); err != nil {
				t.Fatal(err)
			}
			var name, zone string
			if err := db.QueryRow(`SELECT name,timezone FROM cities WHERE id='bf61fbf5-8c5b-53da-96c7-3560df3f8177'`).Scan(&name, &zone); err != nil || name != "Санкт-Петербург" || zone != "Europe/Moscow" {
				t.Fatalf("city=%q zone=%q error=%v", name, zone, err)
			}
			if err := goose.DownTo(db, ".", 33); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM cities WHERE id='bf61fbf5-8c5b-53da-96c7-3560df3f8177'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("Down removed city metadata", err)
			}
		})
	}
}
