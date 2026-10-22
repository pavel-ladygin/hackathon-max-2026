package providers

import (
	"context"
	"errors"
	"testing"
)

func TestSyncDiagnosticPreservesCause(t *testing.T) {
	err := WithSyncStage(context.DeadlineExceeded, "fetch_page")
	if !errors.Is(err, context.DeadlineExceeded) || SyncErrorCode(err) != "provider_timeout" || SyncErrorStage(err) != "fetch_page" {
		t.Fatal(err)
	}
	err = WithSyncStage(errors.New("secret-token"), "finalize_sync")
	if err.Error() != "provider_finalize_sync_failed" {
		t.Fatal(err)
	}
}
