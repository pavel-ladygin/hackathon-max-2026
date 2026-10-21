package migrations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// A separate, explicitly supplied database keeps destructive rollback tests
// isolated from the ordinary integration fixture database.
func TestEventSubmissionMigrationRoundTripAndPublishedDataGuard(t *testing.T) {
	dsn := os.Getenv("TEST_MIGRATION_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_MIGRATION_DATABASE_URL is not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_migration_test") {
		t.Fatal("rollback tests require a dedicated database ending in _migration_test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	// Exercise migration 30 even when newer migrations are present.
	const beforeSubmissionMigration = 29
	if err := goose.DownToContext(ctx, db, ".", beforeSubmissionMigration); err != nil {
		t.Fatalf("down on empty database: %v", err)
	}
	var absent bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('event_submissions') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatalf("submission table should be absent after Down: %v", err)
	}
	if err := Up(ctx, db); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	var kind string
	if err := db.QueryRowContext(ctx, "SELECT source_kind FROM event_sources WHERE source_key='user'").Scan(&kind); err != nil || kind != "system" {
		t.Fatalf("system source after Up = %q, %v", kind, err)
	}
	city, venue, event := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM events WHERE id=$1", event)
		_, _ = db.ExecContext(context.Background(), "DELETE FROM venues WHERE id=$1", venue)
		_, _ = db.ExecContext(context.Background(), "DELETE FROM cities WHERE id=$1", city)
	})
	if _, err := db.ExecContext(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,'Migration fixture','Europe/Moscow',55.75,37.61)`, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO venues(id,city_id,name,address,venue_type) VALUES($1,$2,'Migration fixture','Fixture address','other')`, venue, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO events(id,source,external_id,title,description,venue_id,starts_at,timezone,currency,ticket_available,status) VALUES($1,'user',$2,'Migration fixture','Fixture',$3,now()+interval '1 day','Europe/Moscow','RUB',false,'published')`, event, event.String(), venue); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownToContext(ctx, db, ".", beforeSubmissionMigration); err == nil || !strings.Contains(err.Error(), "cannot roll back user event submissions while published user events exist") {
		t.Fatalf("expected published event rollback guard, got %v", err)
	}
	var intact bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM events WHERE id=$1) AND to_regclass('event_submissions') IS NOT NULL", event).Scan(&intact); err != nil || !intact {
		t.Fatalf("rollback guard must preserve schema and event: %v", err)
	}
}
