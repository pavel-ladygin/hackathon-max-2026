package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type stubImporter struct {
	stats     providers.ImportStats
	err       error
	errs      []error
	recordErr error
	cancel    context.CancelFunc
	calls     int
}

func (s *stubImporter) Import(_ context.Context, _ uuid.UUID, _ providers.SyncStore, reportError func(error)) (providers.ImportStats, error) {
	s.calls++
	if s.recordErr != nil && reportError != nil {
		reportError(s.recordErr)
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.calls <= len(s.errs) {
		return s.stats, s.errs[s.calls-1]
	}
	return s.stats, s.err
}

func TestTemporaryFailureDoesNotBlockNextPeriod(t *testing.T) {
	flaky := &stubImporter{stats: providers.ImportStats{SyncRunID: uuid.New()}, errs: []error{errors.New("temporary outage"), nil}}
	healthy := &stubImporter{stats: providers.ImportStats{SyncRunID: uuid.New()}}
	providersToSync := []syncProvider{{name: "timepad", importer: flaky}, {name: "kudago", importer: healthy}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	for period := 0; period < 2; period++ {
		if err := syncProvidersOnce(context.Background(), uuid.New(), nil, providersToSync, logger); err != nil {
			t.Fatal(err)
		}
	}
	if flaky.calls != 2 || healthy.calls != 2 {
		t.Fatalf("calls after two periods flaky/healthy = %d/%d", flaky.calls, healthy.calls)
	}
	entries := decodeLogEntries(t, logs.String())
	if len(entries) != 4 || entries[0]["final_status"] != "failed" || entries[2]["final_status"] != "succeeded" {
		t.Fatalf("period logs = %+v", entries)
	}
}

func TestSyncProvidersOnceIsolatesFailuresInBothDirections(t *testing.T) {
	for _, test := range []struct {
		name   string
		failed string
		order  []string
	}{
		{name: "timepad failure does not block kudago", failed: "timepad", order: []string{"timepad", "kudago"}},
		{name: "kudago failure does not block timepad", failed: "kudago", order: []string{"kudago", "timepad"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			importers := map[string]*stubImporter{
				"kudago":  {stats: providers.ImportStats{SyncRunID: uuid.New(), Fetched: 2, Inserted: 2}},
				"timepad": {stats: providers.ImportStats{SyncRunID: uuid.New(), Fetched: 2, Inserted: 2}},
			}
			importers[test.failed].err = errors.New("provider unavailable")
			providersToSync := make([]syncProvider, 0, 2)
			for _, name := range test.order {
				providersToSync = append(providersToSync, syncProvider{name: name, importer: importers[name]})
			}
			if err := syncProvidersOnce(context.Background(), uuid.New(), nil, providersToSync, logger); err != nil {
				t.Fatal(err)
			}
			if importers["kudago"].calls != 1 || importers["timepad"].calls != 1 {
				t.Fatalf("provider calls kudago/timepad = %d/%d", importers["kudago"].calls, importers["timepad"].calls)
			}
			entries := decodeLogEntries(t, logs.String())
			if len(entries) != 2 || entries[0]["final_status"] != "failed" || entries[1]["final_status"] != "succeeded" {
				t.Fatalf("final logs = %+v", entries)
			}
		})
	}
}

func TestSyncOneLogsStructuredFinalStatusAndNoSensitiveErrors(t *testing.T) {
	runID := uuid.New()
	tests := []struct {
		name       string
		importer   *stubImporter
		wantStatus string
		wantError  bool
	}{
		{name: "succeeded", importer: &stubImporter{stats: providers.ImportStats{SyncRunID: runID, PagesFetched: 2, Fetched: 4, Matched: 3, Normalized: 2, Inserted: 1, Updated: 1, Skipped: 2, Reconciled: 5}}, wantStatus: "succeeded"},
		{name: "partial record failure", importer: &stubImporter{stats: providers.ImportStats{SyncRunID: runID, PagesFetched: 1, Errors: 1}, recordErr: errors.New("secret-token raw-private-payload")}, wantStatus: "failed", wantError: true},
		{name: "provider failure", importer: &stubImporter{stats: providers.ImportStats{SyncRunID: runID}, err: errors.New("secret-token raw-private-payload")}, wantStatus: "failed", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			err := syncOne(context.Background(), uuid.New(), nil, syncProvider{name: "timepad", priority: timepadPriority, importer: test.importer}, logger)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError %t", err, test.wantError)
			}
			if strings.Contains(logs.String(), "secret-token") || strings.Contains(logs.String(), "raw-private-payload") {
				t.Fatalf("sensitive provider error leaked to logs: %s", logs.String())
			}
			entries := decodeLogEntries(t, logs.String())
			final := entries[len(entries)-1]
			for _, key := range []string{"provider", "sync_run_id", "pages_fetched", "fetched", "matched", "normalized", "inserted", "updated", "skipped", "errors", "reconciled", "inactivated", "duration", "final_status"} {
				if _, exists := final[key]; !exists {
					t.Errorf("final log lacks %q: %+v", key, final)
				}
			}
			if final["sync_run_id"] != runID.String() || final["final_status"] != test.wantStatus {
				t.Fatalf("final log = %+v", final)
			}
			if test.wantStatus == "failed" && final["reconciled"] != float64(0) {
				t.Fatalf("failed sync reported reconciliation: %+v", final)
			}
		})
	}
}

func TestSyncProvidersOnceStopsOnProcessCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	first := &stubImporter{err: context.Canceled, cancel: cancel, stats: providers.ImportStats{SyncRunID: uuid.New()}}
	second := &stubImporter{}
	var logs bytes.Buffer
	err := syncProvidersOnce(ctx, uuid.New(), nil, []syncProvider{{name: "timepad", importer: first}, {name: "kudago", importer: second}}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if !errors.Is(err, context.Canceled) || first.calls != 1 || second.calls != 0 {
		t.Fatalf("cancellation result err=%v calls=%d/%d", err, first.calls, second.calls)
	}
	entries := decodeLogEntries(t, logs.String())
	if len(entries) != 1 || entries[0]["final_status"] != "cancelled" {
		t.Fatalf("cancellation logs = %+v", entries)
	}
}

func decodeLogEntries(t *testing.T, raw string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode log %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}
