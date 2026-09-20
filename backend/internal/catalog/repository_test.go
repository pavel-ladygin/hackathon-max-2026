package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

func TestLoadCityUsesDBTXFromContext(t *testing.T) {
	wantErr := errors.New("bound transaction query")
	dbtx := &loadCityDBTX{row: errorRow{err: wantErr}}
	repo := &Repository{db: &store.Pool{}}

	_, err := repo.LoadCity(store.WithDBTX(context.Background(), dbtx), uuid.New())
	if !errors.Is(err, wantErr) {
		t.Fatalf("LoadCity() error = %v, want %v", err, wantErr)
	}
	if dbtx.queryRowCalls != 1 {
		t.Fatalf("bound DBTX QueryRow calls = %d, want 1", dbtx.queryRowCalls)
	}
}

type loadCityDBTX struct {
	row           pgx.Row
	queryRowCalls int
}

func (d *loadCityDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (d *loadCityDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }

func (d *loadCityDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	d.queryRowCalls++
	return d.row
}

type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }
