package timepad

import (
	"context"
	"errors"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"testing"
)

func TestSafeRequestDiagnostics(t *testing.T) {
	for _, status := range []int{403, 429} {
		if providers.SyncErrorCode(httpFailure(status)) == "" {
			t.Fatal(status)
		}
	}
	err := requestFailure(context.DeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) || providers.SyncErrorCode(err) != "timepad_timeout" {
		t.Fatal(err)
	}
	if decodeFailure(errors.New("secret-token")).Error() != "timepad_decode_error" {
		t.Fatal("unsafe decode error")
	}
}
