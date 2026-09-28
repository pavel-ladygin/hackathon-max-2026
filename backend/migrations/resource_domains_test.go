package migrations

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestResourceDomainsMigrationUpDownAgainstPostgres(t *testing.T) {
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
	schema := "resource_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = tx.Exec(ctx, `CREATE SCHEMA `+schema+`; SET LOCAL search_path TO `+schema+`; CREATE TABLE event_sources(id uuid PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	data, err := Files.ReadFile("000024_event_source_allowed_domains.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing migration down")
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	source := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO event_sources(id) VALUES($1)`, source); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO event_source_allowed_domains(id,source_id,hostname,purpose) VALUES($1,$2,'cdn.example.test','image')`, uuid.New(), source); err != nil {
		t.Fatal(err)
	}
	for _, hostname := range []string{"CDN.example.test", "cdn.example.test.", "*.example.test", "127.0.0.1", "https://cdn.example.test/a"} {
		if _, err = tx.Exec(ctx, "SAVEPOINT invalid_hostname"); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO event_source_allowed_domains(id,source_id,hostname,purpose) VALUES($1,$2,$3,'image')`, uuid.New(), source, hostname)
		if err == nil {
			t.Fatalf("DB accepted noncanonical hostname %q", hostname)
		}
		if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT invalid_hostname"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatalf("migration down: %v", err)
	}
	var table *string
	if err = tx.QueryRow(ctx, `SELECT to_regclass($1)::text`, schema+".event_source_allowed_domains").Scan(&table); err != nil || table != nil {
		t.Fatalf("down left table %v: %v", table, err)
	}
}
