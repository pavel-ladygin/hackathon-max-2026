package providers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

type syncSafetyStore struct {
	finish      SyncRunFinish
	finishCalls int
	finishErr   error
}

func (s *syncSafetyStore) UpsertWithResult(context.Context, uuid.UUID, NormalizedEvent) (UpsertResult, error) {
	return UpsertResult{}, errors.New("not used")
}

func (s *syncSafetyStore) BeginSyncRun(context.Context, SyncRunStart) (uuid.UUID, error) {
	return uuid.Nil, errors.New("not used")
}

func (s *syncSafetyStore) FinishSyncRun(_ context.Context, finish SyncRunFinish) (int, error) {
	s.finishCalls++
	s.finish = finish
	return 0, s.finishErr
}

type unsafeSyncError struct{ text string }

func (e unsafeSyncError) Error() string         { return e.text }
func (e unsafeSyncError) SyncErrorCode() string { return e.text }

func TestSyncErrorCodeAllowlistAndRedaction(t *testing.T) {
	secret := "Bearer very-private-token raw provider payload"
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "allowlisted safe code", err: SafeError("provider_city_mismatch"), want: "provider_city_mismatch"},
		{name: "HTTP status", err: HTTPError(429), want: "provider_http_429"},
		{name: "wrapped HTTP status", err: fmt.Errorf("fetch: %w", HTTPError(502)), want: "provider_http_502"},
		{name: "arbitrary error", err: errors.New(secret), want: "provider_import_error"},
		{name: "unsafe coded error", err: unsafeSyncError{text: secret}, want: "provider_import_error"},
		{name: "unsupported safe constructor input", err: SafeError(secret), want: "provider_import_error"},
		{name: "invalid HTTP status", err: HTTPError(700), want: "provider_import_error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SyncErrorCode(test.err); got != test.want {
				t.Fatalf("SyncErrorCode(%v) = %q, want %q", test.err, got, test.want)
			}
			if got := SyncErrorCode(test.err); got == secret {
				t.Fatal("SyncErrorCode returned provider diagnostic text")
			}
		})
	}
}

func TestFinalizeSyncRunRedactsFailureAndPreventsSuccessfulFinish(t *testing.T) {
	store := &syncSafetyStore{}
	runID := uuid.New()
	secret := "Authorization: Bearer token-should-never-reach-audit"

	if _, err := FinalizeSyncRun(context.Background(), store, runID, ImportStats{}, unsafeSyncError{text: secret}); err != nil {
		t.Fatalf("FinalizeSyncRun() error = %v", err)
	}
	if store.finishCalls != 1 {
		t.Fatalf("FinishSyncRun calls = %d, want 1", store.finishCalls)
	}
	if store.finish.RunID != runID || store.finish.State != SyncRunFailed {
		t.Fatalf("finish = %#v, want failed finish for %s", store.finish, runID)
	}
	if store.finish.ErrorText != "provider_import_error" || store.finish.ErrorText == secret {
		t.Fatalf("finish error text = %q, want redacted provider_import_error", store.finish.ErrorText)
	}
}

func TestFinalizeSyncRunPersistenceFailurePreventsSuccessfulFinish(t *testing.T) {
	store := &syncSafetyStore{}
	if _, err := FinalizeSyncRun(context.Background(), store, uuid.New(), ImportStats{Errors: 1}, nil); err != nil {
		t.Fatalf("FinalizeSyncRun() error = %v", err)
	}
	if store.finish.State != SyncRunFailed || store.finish.ErrorText != "provider_persistence_error" {
		t.Fatalf("finish = %#v, want failed persistence finish", store.finish)
	}
}

func TestFinalizeSyncRunRedactsFinalizationFailure(t *testing.T) {
	store := &syncSafetyStore{finishErr: errors.New("database password is secret")}
	if _, err := FinalizeSyncRun(context.Background(), store, uuid.New(), ImportStats{}, nil); SyncErrorCode(err) != "provider_finalization_error" {
		t.Fatalf("FinalizeSyncRun error code = %q, want provider_finalization_error", SyncErrorCode(err))
	}
}
