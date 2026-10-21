package providers

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"testing"
	"time"
)

func TestMulticityPaddedSourceMappingLockedAfterImport(t *testing.T) {
	db := openMulticityIntegrationDB(t)
	ctx := context.Background()
	cityID := insertMulticityCities(t, db, 1)[0]
	codec, err := sourceconfig.NewSecretCodec(make([]byte, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{" ", "\t\n", "\u00a0"} {
		t.Run(suffix, func(t *testing.T) {
			key := "generic:mapping-" + uuid.NewString()
			input := sourceconfig.Input{SourceKey: key + suffix, Name: "fixture", EndpointURL: "https://example.test", AuthType: "none", Defaults: sourceconfig.Defaults{Category: "other", Timezone: "Europe/Moscow", Currency: "RUB", Status: "published"}, PriceUnit: "major"}
			source, err := sources.Create(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			repo := NewRepository(db)
			run, err := repo.BeginSyncRun(ctx, SyncRunStart{Provider: key, CityID: cityID, WindowStart: time.Now().UTC(), WindowEnd: time.Now().UTC().Add(time.Hour), UpsertOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.FinishSyncRun(ctx, SyncRunFinish{RunID: run, State: SyncRunSucceeded}); err != nil {
				t.Fatal(err)
			}
			stored, err := sources.Get(ctx, source.ID)
			if err != nil || !stored.MappingLocked {
				t.Fatalf("mapping remains editable: %v", err)
			}
			input.Mapping = json.RawMessage(`{"external_id":{"path":"changed_id"}}`)
			if _, err := sources.Update(ctx, source.ID, input); !errors.Is(err, sourceconfig.ErrMappingImmutable) {
				t.Fatalf("locked mapping edit: %v", err)
			}
		})
	}
}
