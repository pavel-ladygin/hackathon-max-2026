package testdbguard

import (
	"os"
	"strings"
	"testing"
)

const fixtureDSN = "postgres://fixture_user@127.0.0.1:65428/multicity_fixture?sslmode=disable"

func TestParseAcceptsOnlyTheExactDisposableFixture(t *testing.T) {
	clearPGEnvironment(t)

	cfg, err := Parse(fixtureDSN, "multicity_fixture")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 65428 || cfg.Database != "multicity_fixture" {
		t.Fatalf("unexpected parsed target: host=%q port=%d database=%q", cfg.Host, cfg.Port, cfg.Database)
	}
	if cfg.TLSConfig != nil || len(cfg.Fallbacks) != 0 {
		t.Fatalf("unsafe parsed transport: TLS=%v fallbacks=%d", cfg.TLSConfig != nil, len(cfg.Fallbacks))
	}
}

func TestParseAcceptsApprovedGeneratedMigrationDatabase(t *testing.T) {
	clearPGEnvironment(t)
	const migrationDatabase = "multicity_migration_city_metadata_7"
	dsn := strings.Replace(fixtureDSN, "multicity_fixture", migrationDatabase, 1)

	cfg, err := Parse(dsn, migrationDatabase)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Database != migrationDatabase {
		t.Fatalf("database = %q, want %q", cfg.Database, migrationDatabase)
	}
}

func TestParseRejectsUnsafeDSNsBeforeAnyConnection(t *testing.T) {
	clearPGEnvironment(t)

	for name, dsn := range map[string]string{
		"keyword DSN":                  "host=127.0.0.1 port=65428 user=fixture_user dbname=multicity_fixture sslmode=disable",
		"service":                      "postgres://fixture_user@127.0.0.1:65428/multicity_fixture?sslmode=disable&service=fixture",
		"host override":                "postgres://fixture_user@localhost:65428/multicity_fixture?sslmode=disable",
		"port override":                "postgres://fixture_user@127.0.0.1:65426/multicity_fixture?sslmode=disable",
		"database override":            "postgres://fixture_user@127.0.0.1:65428/postgres?sslmode=disable",
		"missing user":                 "postgres://127.0.0.1:65428/multicity_fixture?sslmode=disable",
		"missing sslmode":              "postgres://fixture_user@127.0.0.1:65428/multicity_fixture",
		"TLS enabled":                  "postgres://fixture_user@127.0.0.1:65428/multicity_fixture?sslmode=require",
		"runtime override":             "postgres://fixture_user@127.0.0.1:65428/multicity_fixture?sslmode=disable&application_name=unsafe",
		"multiple hosts":               "postgres://fixture_user@127.0.0.1,localhost:65428/multicity_fixture?sslmode=disable",
		"unix socket":                  "postgres://fixture_user@%2Fvar%2Frun%2Fpostgresql:65428/multicity_fixture?sslmode=disable",
		"fragment":                     "postgres://fixture_user@127.0.0.1:65428/multicity_fixture?sslmode=disable#unsafe",
		"escaped database path":        "postgres://fixture_user@127.0.0.1:65428/multicity%5Ffixture?sslmode=disable",
		"unapproved expected database": fixtureDSN,
	} {
		t.Run(name, func(t *testing.T) {
			expectedDatabase := "multicity_fixture"
			if name == "unapproved expected database" {
				expectedDatabase = "postgres"
			}
			if _, err := Parse(dsn, expectedDatabase); err == nil {
				t.Fatal("Parse() accepted unsafe DSN")
			}
		})
	}
}

func TestParseRejectsPGEnvironmentOverrides(t *testing.T) {
	for name, value := range map[string]string{
		"PGHOST":     "localhost",
		"PGPORT":     "5432",
		"PGDATABASE": "postgres",
		"PGSERVICE":  "unsafe",
		"pgservice":  "unsafe",
	} {
		t.Run(name, func(t *testing.T) {
			clearPGEnvironment(t)
			t.Setenv(name, value)
			if _, err := Parse(fixtureDSN, "multicity_fixture"); err == nil {
				t.Fatalf("Parse() accepted %s override", name)
			}
		})
	}
}

func clearPGEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(strings.ToUpper(name), "PG") {
			t.Setenv(name, "")
		}
	}
}
