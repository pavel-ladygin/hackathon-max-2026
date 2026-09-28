package migrations

import (
	"slices"
	"strings"
	"testing"
)

func TestExpectedVersionsIncludesFoundation(t *testing.T) {
	versions, err := ExpectedVersions()
	if err != nil {
		t.Fatal(err)
	}
	// The frozen foundation versions must remain present when later features
	// append migrations. In particular, a broken filename parser must not make
	// an unmigrated database appear current.
	want := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if len(versions) < len(want) || !slices.Equal(versions[:len(want)], want) {
		t.Fatalf("foundation versions = %v, want prefix %v", versions, want)
	}
	if versions[len(versions)-1] != 24 {
		t.Fatalf("latest migration version = %d, want 24", versions[len(versions)-1])
	}
}

func TestEventSourceAllowedDomainsMigrationContainsUpAndDown(t *testing.T) {
	data, err := Files.ReadFile("000024_event_source_allowed_domains.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, required := range []string{
		"-- +goose Up", "CREATE TABLE event_source_allowed_domains", "REFERENCES event_sources(id) ON DELETE CASCADE",
		"UNIQUE (source_id, hostname, purpose)", "CHECK (purpose IN ('image','ticket'))", "-- +goose Down", "DROP TABLE event_source_allowed_domains",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration is missing %q", required)
		}
	}
}
