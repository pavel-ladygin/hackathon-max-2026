package rooms

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRetryableRoomTxError(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		if !retryableRoomTxError(&pgconn.PgError{Code: code}) {
			t.Fatalf("SQLSTATE %s must be retryable", code)
		}
	}
	if retryableRoomTxError(&pgconn.PgError{Code: "23505"}) || retryableRoomTxError(errors.New("builder failed")) {
		t.Fatal("non-transaction error marked retryable")
	}
}
