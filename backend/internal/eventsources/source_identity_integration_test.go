package eventsources

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
)

func TestCanonicalSourceCollisionsAreRejectedBeforeImport(t *testing.T) {
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
	t.Cleanup(db.Close)
	codec, err := sourceconfig.NewSecretCodec(make([]byte, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	input := validSourceInput()
	input.SourceKey = "generic:canonical-" + uuid.NewString()
	first, err := sources.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	legacyID := uuid.New()
	duplicate := input
	duplicate.SourceKey += "\t "
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, `DELETE FROM event_sources WHERE id=ANY($1::uuid[])`, []uuid.UUID{first.ID, legacyID})
	})
	if _, err := sources.Create(ctx, duplicate); !errors.Is(err, sourceconfig.ErrCanonicalIdentityConflict) {
		t.Errorf("duplicate error = %v, want canonical identity conflict", err)
	}
	// A pre-existing raw-key collision may have been created by an old binary.
	// Insert that historical state directly, then require fail-closed execution.
	if _, err := db.Exec(ctx, `INSERT INTO event_sources(id,source_key,name,enabled,endpoint_url,auth_type,key_version,default_category,default_timezone,default_currency,default_status,price_unit)
 VALUES($1,$2,'Legacy duplicate',true,'https://example.test','none',1,'other','Europe/Moscow','RUB','published','major')`, legacyID, input.SourceKey+"\u00a0"); err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(db, sources, uuid.MustParse(catalogseed.MoscowCityID))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	runner.importFn = func(context.Context, uuid.UUID, sourceconfig.Source, string, providers.SyncStore) (providers.ImportStats, error) {
		called = true
		return providers.ImportStats{}, nil
	}
	if _, err := runner.Run(ctx, first.ID); !errors.Is(err, sourceconfig.ErrCanonicalIdentityConflict) || called {
		t.Fatal("ambiguous provider identity reached manual importer")
	}
	if err := runner.Start(ctx, first.ID); !errors.Is(err, sourceconfig.ErrCanonicalIdentityConflict) {
		t.Fatal("ambiguous provider identity scheduled")
	}
}

func TestCanonicalSourceConcurrentCreateRejectsOne(t *testing.T) {
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
	t.Cleanup(db.Close)
	codec, err := sourceconfig.NewSecretCodec(make([]byte, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	key := "generic:concurrent-" + uuid.NewString()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, `DELETE FROM event_sources WHERE source_key=ANY($1::text[])`, []string{key, key + "\u00a0"})
	})

	inputs := []sourceconfig.Input{validSourceInput(), validSourceInput()}
	inputs[0].SourceKey = key
	inputs[1].SourceKey = key + "\u00a0"
	start := make(chan struct{})
	results := make(chan error, len(inputs))
	var wait sync.WaitGroup
	for _, input := range inputs {
		wait.Add(1)
		go func(input sourceconfig.Input) {
			defer wait.Done()
			<-start
			_, err := sources.Create(ctx, input)
			results <- err
		}(input)
	}
	close(start)
	wait.Wait()
	close(results)
	created, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, sourceconfig.ErrCanonicalIdentityConflict):
			conflicts++
		default:
			t.Fatalf("Create() error = %v", err)
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("created=%d conflicts=%d, want one of each", created, conflicts)
	}
}
