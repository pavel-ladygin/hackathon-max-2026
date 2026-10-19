package migrations

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRoomPublicPlanMigrationUpDownAndLegacyRows(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	schema := "room_public_plan_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := tx.Exec(ctx, `CREATE SCHEMA `+schema+`; SET LOCAL search_path TO `+schema+`; CREATE TABLE rooms(id uuid PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	data, err := Files.ReadFile("000025_room_public_plan.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration must contain goose Up and Down sections")
	}
	up, down := strings.TrimSpace(strings.TrimPrefix(parts[0], "-- +goose Up")), strings.TrimSpace(parts[1])
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	legacyID, plannedID := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO rooms(id) VALUES($1),($2)`, legacyID, plannedID); err != nil {
		t.Fatal(err)
	}
	var legacyPlan *string
	if err := tx.QueryRow(ctx, `SELECT public_plan::text FROM rooms WHERE id=$1`, legacyID).Scan(&legacyPlan); err != nil || legacyPlan != nil {
		t.Fatalf("legacy row plan=%v err=%v; want SQL NULL", legacyPlan, err)
	}
	const plan = `{"dates":["2026-10-24"],"time_slots":["evening"],"category_slugs":["concerts"],"budget_max_minor":250000}`
	if _, err := tx.Exec(ctx, `UPDATE rooms SET public_plan=$2::jsonb WHERE id=$1`, plannedID, plan); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := tx.QueryRow(ctx, `SELECT public_plan::text FROM rooms WHERE id=$1`, plannedID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var wantPlan, gotPlan any
	if err := json.Unmarshal([]byte(plan), &wantPlan); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(stored), &gotPlan); err != nil || !reflect.DeepEqual(gotPlan, wantPlan) {
		t.Fatalf("stored plan=%q err=%v; want semantic JSON %q", stored, err, plan)
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT invalid_public_plan`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE rooms SET public_plan='[]'::jsonb WHERE id=$1`, plannedID); err == nil {
		t.Fatal("migration constraint accepted a non-object public_plan")
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_public_plan`); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatalf("migration down: %v", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='rooms' AND column_name='public_plan')`, schema).Scan(&exists); err != nil || exists {
		t.Fatalf("down left public_plan column exists=%v err=%v", exists, err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("migration re-up: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT public_plan::text FROM rooms WHERE id=$1`, legacyID).Scan(&legacyPlan); err != nil || legacyPlan != nil {
		t.Fatalf("legacy row after re-up plan=%v err=%v; want SQL NULL", legacyPlan, err)
	}
}
