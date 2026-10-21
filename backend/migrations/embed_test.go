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
	for _, required := range []int64{25, 30, 31, 32} {
		if !slices.Contains(versions, required) {
			t.Fatalf("required migration %d is missing: %v", required, versions)
		}
	}
}

func TestEventSubmissionsMigrationContainsTransactionalWorkflowAndSafeDown(t *testing.T) {
	data, err := Files.ReadFile("000030_event_submissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, required := range []string{
		"-- +goose Up", "CREATE TABLE event_submissions", "UNIQUE (author_id, idempotency_key)",
		"CREATE INDEX event_submissions_queue_idx", "CREATE TRIGGER event_submissions_transition_guard",
		"'11111111-1111-5111-8111-111111111130'", "'user'", "'system'", "-- +goose Down",
		"cannot roll back user event submissions while published user events exist",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration is missing %q", required)
		}
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
