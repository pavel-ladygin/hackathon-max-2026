package timepad

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestImportSnapshotFilesSkipsMalformedEventsAndIsIdempotent(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "page-0.json")
	second := filepath.Join(directory, "page-100.json")
	if err := os.WriteFile(first, []byte(`{"total":3,"values":[`+timepadImportEvent(1, "Москва")+`,{"id":{}},`+timepadImportEvent(2, "Онлайн")+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(`{"total":3,"values":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &memoryEventStore{seen: make(map[string]struct{})}
	paths := []string{first, second}

	firstStats, err := ImportSnapshotFiles(context.Background(), paths, uuid.New(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := (ImportStats{PagesFetched: 2, Fetched: 3, Matched: 1, Normalized: 1, Inserted: 1, Skipped: 2, Errors: 1})
	if firstStats != wantFirst {
		t.Fatalf("first stats = %+v, want %+v", firstStats, wantFirst)
	}

	secondStats, err := ImportSnapshotFiles(context.Background(), paths, uuid.New(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantSecond := wantFirst
	wantSecond.Inserted = 0
	wantSecond.Updated = 1
	if secondStats != wantSecond {
		t.Fatalf("second stats = %+v, want %+v", secondStats, wantSecond)
	}
}
