// Package testdbguard validates disposable PostgreSQL targets before tests connect.
package testdbguard

import (
	"errors"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	fixtureHost = "127.0.0.1"
	fixturePort = uint16(65428)
)

// Parse accepts only the explicitly provisioned disposable PostgreSQL fixture.
// It does not connect to a database.
func Parse(dsn, expectedDatabase string) (*pgx.ConnConfig, error) {
	if !validExpectedDatabase(expectedDatabase) {
		return nil, errors.New("test database name is not an approved disposable target")
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return nil, errors.New("test database URL is invalid")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, errors.New("test database URL must use postgres scheme")
	}
	if u.Opaque != "" || u.Fragment != "" || u.ForceQuery || u.Host != fixtureHost+":65428" ||
		u.Hostname() != fixtureHost || u.Port() != "65428" || u.Path != "/"+expectedDatabase ||
		u.EscapedPath() != "/"+expectedDatabase {
		return nil, errors.New("test database URL must target the disposable fixture")
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, errors.New("test database URL must include a user")
	}
	if u.RawQuery != "sslmode=disable" {
		return nil, errors.New("test database URL must set only sslmode=disable")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["sslmode"]) != 1 || query.Get("sslmode") != "disable" {
		return nil, errors.New("test database URL query is invalid")
	}
	if hasPGEnvironmentOverride() {
		return nil, errors.New("PG environment variables are not allowed for disposable database tests")
	}

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("test database URL is invalid")
	}
	if cfg.Host != fixtureHost || cfg.Port != fixturePort || cfg.Database != expectedDatabase ||
		cfg.User != u.User.Username() || cfg.TLSConfig != nil || len(cfg.Fallbacks) != 0 {
		return nil, errors.New("parsed test database configuration is unsafe")
	}
	return cfg, nil
}

func validExpectedDatabase(name string) bool {
	if name == "multicity_fixture" {
		return true
	}
	const migrationPrefix = "multicity_migration_"
	if !strings.HasPrefix(name, migrationPrefix) || len(name) == len(migrationPrefix) {
		return false
	}
	for _, r := range name[len(migrationPrefix):] {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	return true
}

func hasPGEnvironmentOverride() bool {
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(strings.ToUpper(name), "PG") && value != "" {
			return true
		}
	}
	return false
}
