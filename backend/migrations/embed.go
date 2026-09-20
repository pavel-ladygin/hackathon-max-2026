// Package migrations exposes the application's embedded database migrations.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

// Files contains every versioned SQL migration shipped with this binary.
//
//go:embed *.sql
var Files embed.FS

var migrationFilename = regexp.MustCompile(`^(\d+)_.*\.sql$`)

// ExpectedVersions returns the complete set of migration versions shipped with the binary.
func ExpectedVersions() ([]int64, error) {
	entries, err := fs.ReadDir(Files, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	versions := make([]int64, 0, len(entries))
	seen := make(map[int64]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationFilename.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", entry.Name(), err)
		}
		if _, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d", version)
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions, nil
}

// Up applies embedded migrations to db. The server never invokes this function.
func Up(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(Files)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// CheckCurrent confirms that every embedded version, and only those versions, is applied.
func CheckCurrent(ctx context.Context, db *pgxpool.Pool) error {
	expected, err := ExpectedVersions()
	if err != nil {
		return err
	}
	rows, err := db.Query(ctx, `
		SELECT DISTINCT ON (version_id) version_id, is_applied
		FROM goose_db_version
		ORDER BY version_id, id DESC`)
	if err != nil {
		return fmt.Errorf("query migration state: %w", err)
	}
	defer rows.Close()
	applied := make(map[int64]bool)
	for rows.Next() {
		var version int64
		var isApplied bool
		if err := rows.Scan(&version, &isApplied); err != nil {
			return fmt.Errorf("scan migration state: %w", err)
		}
		if version != 0 {
			applied[version] = isApplied
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate migration state: %w", err)
	}
	if len(applied) != len(expected) {
		return fmt.Errorf("migration version set differs from embedded migrations")
	}
	for _, version := range expected {
		if !applied[version] {
			return fmt.Errorf("migration %d is not applied", version)
		}
	}
	return nil
}
