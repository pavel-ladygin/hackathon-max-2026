package eventsources

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
	"os"
	"testing"
)

func TestSystemSourceCannotExecuteGenericImport(t *testing.T) {
	dsn := os.Getenv("MULTICITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated multicity DB required")
	}
	if _, err := testdbguard.Parse(dsn, "multicity_fixture"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	codec, err := sourceconfig.NewSecretCodec(make([]byte, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	systemID := uuid.MustParse("11111111-1111-5111-8111-111111111130")
	var before string
	if err := db.QueryRow(ctx, `SELECT to_jsonb(s)::text FROM event_sources s WHERE id=$1`, systemID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	list, err := sources.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.SourceKey == "user" || s.ID == systemID {
			t.Fatal("system source exposed by generic List")
		}
	}
	if _, err := sources.Get(ctx, systemID); !errors.Is(err, sourceconfig.ErrNotFound) {
		t.Fatalf("system Get: %v", err)
	}
	runner, err := NewRunner(db, sources, uuid.MustParse(catalogseed.MoscowCityID))
	if err != nil {
		t.Fatal(err)
	}
	runner.importFn = func(context.Context, uuid.UUID, sourceconfig.Source, string, providers.SyncStore) (providers.ImportStats, error) {
		t.Fatal("system source reached external importer")
		return providers.ImportStats{}, nil
	}
	if _, err := runner.Run(ctx, systemID); !errors.Is(err, sourceconfig.ErrNotFound) {
		t.Fatalf("manual Run: %v", err)
	}
	if err := runner.Start(ctx, systemID); !errors.Is(err, sourceconfig.ErrNotFound) {
		t.Fatalf("scheduled Start: %v", err)
	}
	var after string
	var runs int
	if err := db.QueryRow(ctx, `SELECT to_jsonb(s)::text FROM event_sources s WHERE id=$1`, systemID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM provider_sync_runs WHERE provider='user'`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if before != after || runs != 0 {
		t.Fatal("system source or sync state changed")
	}
}
