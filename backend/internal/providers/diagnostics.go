package providers

import (
	"context"
	"errors"
)

type stageError struct {
	stage string
	err   error
}

func (e *stageError) Error() string          { return e.SyncErrorCode() }
func (e *stageError) Unwrap() error          { return e.err }
func (e *stageError) SyncErrorStage() string { return e.stage }
func (e *stageError) SyncErrorCode() string {
	if code := SyncErrorCode(e.err); code != "" {
		return code
	}
	if errors.Is(e.err, context.DeadlineExceeded) {
		return "provider_timeout"
	}
	if errors.Is(e.err, context.Canceled) {
		return "provider_cancelled"
	}
	return "provider_" + e.stage + "_failed"
}
func WithSyncStage(err error, stage string) error {
	if err == nil {
		return nil
	}
	switch stage {
	case "begin_sync", "fetch_page", "finalize_sync", "persist_record", "poster_resolve", "poster_fetch", "poster_persist", "import":
	default:
		stage = "import"
	}
	return &stageError{stage: stage, err: err}
}
func SyncErrorStage(err error) string {
	var staged interface{ SyncErrorStage() string }
	if errors.As(err, &staged) {
		return staged.SyncErrorStage()
	}
	return ""
}
