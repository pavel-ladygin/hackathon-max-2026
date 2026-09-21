package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDBTXContextRoundTrip(t *testing.T) {
	var dbtx DBTX = testDBTX{}
	ctx := WithDBTX(context.Background(), dbtx)
	if got := DBTXFromContext(ctx); got != dbtx {
		t.Fatalf("DBTXFromContext() = %T, want original DBTX", got)
	}
}

func TestDBTXFromContextAbsent(t *testing.T) {
	if got := DBTXFromContext(context.Background()); got != nil {
		t.Fatalf("DBTXFromContext() = %T, want nil", got)
	}
}

type testDBTX struct{}

func (testDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (testDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }

func (testDBTX) QueryRow(context.Context, string, ...any) pgx.Row { return nil }
