package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type stubImporter struct {
	stats providers.ImportStats
	err   error
	calls int
}

func (s *stubImporter) Import(context.Context, uuid.UUID, providers.SyncStore, func(error)) (providers.ImportStats, error) {
	s.calls++
	return s.stats, s.err
}

func TestSyncOneReportsProviderFailureWithoutBlockingOtherProvider(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	failed := &stubImporter{err: errors.New("Timepad unavailable")}
	succeeded := &stubImporter{stats: providers.ImportStats{Fetched: 2, Inserted: 2}}

	err := syncOne(context.Background(), uuid.New(), nil, syncProvider{name: "timepad", priority: timepadPriority, importer: failed}, logger)
	if !errors.Is(err, failed.err) || failed.calls != 1 {
		t.Fatalf("failure result: error=%v calls=%d", err, failed.calls)
	}
	if err := syncOne(context.Background(), uuid.New(), nil, syncProvider{name: "kudago", priority: kudaGoPriority, importer: succeeded}, logger); err != nil {
		t.Fatal(err)
	}
	if succeeded.calls != 1 {
		t.Fatalf("healthy provider calls = %d, want 1", succeeded.calls)
	}
	if !bytes.Contains(logs.Bytes(), []byte("event sync provider failed")) || !bytes.Contains(logs.Bytes(), []byte("event sync provider completed")) {
		t.Fatalf("expected failure and completion logs, got %s", logs.String())
	}
}
