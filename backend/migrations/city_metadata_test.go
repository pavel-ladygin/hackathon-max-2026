package migrations

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
)

func TestKazanMetadataMigrationIsIdempotentAndRejectsConflicts(t *testing.T) {
	dsn := os.Getenv("MULTICITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MULTICITY_TEST_DATABASE_URL is not set")
	}
	cfg, err := testdbguard.Parse(dsn, "multicity_fixture")
	if err != nil {
		t.Fatal("migration test requires the approved isolated multicity fixture")
	}
	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	defer conn.Close(ctx)
	raw, err := Files.ReadFile("000031_multicity_city.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(raw), "-- +goose Down")[0]
	for _, scenario := range []string{"replay", "semantic_duplicate", "uuid_collision"} {
		t.Run(scenario, func(t *testing.T) {
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			switch scenario {
			case "semantic_duplicate":
				_, err = tx.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng)
					VALUES($1,'Казань','Europe/Moscow',55.78874,49.12214)`, uuid.New())
			case "uuid_collision":
				_, err = tx.Exec(ctx, `UPDATE cities SET name='Fixture different city'
					WHERE id='3d3c83de-691b-510e-85e5-79685c4012ee'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, up)
			if scenario == "replay" {
				if err != nil {
					t.Fatal(err)
				}
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM cities WHERE id='3d3c83de-691b-510e-85e5-79685c4012ee'`).Scan(&n); err != nil || n != 1 {
					t.Fatal("metadata replay duplicated or removed the city")
				}
			} else if err == nil {
				t.Fatal("conflicting city identity silently accepted")
			}
		})
	}
}
