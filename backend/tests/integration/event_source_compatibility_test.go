package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
)

func TestEventSourceSecretCompatibilityFailsBeforeUsingMismatchedKey(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	key := []byte("0123456789abcdef0123456789abcdef")
	codec, err := sourceconfig.NewSecretCodec(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	sourceKey := "generic:compat-" + uuid.NewString()
	source, err := repo.Create(ctx, secretCompatibilityInput(sourceKey, "credential-that-must-not-leak"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(ctx, `DELETE FROM event_sources WHERE id=$1`, source.ID) })
	_, gotSecret, err := repo.GetForExecution(ctx, source.ID)
	if err != nil || gotSecret != "credential-that-must-not-leak" {
		t.Fatalf("current key/version should decrypt this source: got secret match=%t, err=%v", gotSecret == "credential-that-must-not-leak", err)
	}

	for _, test := range []struct {
		name    string
		key     []byte
		version int16
	}{
		{name: "version changed", key: key, version: 2},
		{name: "master key changed", key: []byte("abcdef0123456789abcdef0123456789"), version: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "version changed" {
				if _, err := db.Exec(ctx, `UPDATE event_sources SET key_version=2 WHERE id=$1`, source.ID); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = db.Exec(ctx, `UPDATE event_sources SET key_version=1 WHERE id=$1`, source.ID) })
			}
			wrongCodec, err := sourceconfig.NewSecretCodec(test.key, test.version)
			if err != nil {
				t.Fatal(err)
			}
			wrongRepo, err := sourceconfig.NewRepository(db, wrongCodec)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := wrongRepo.GetForExecution(ctx, source.ID); err == nil {
				t.Fatal("source credential was readable with mismatched key/version")
			}
			err = wrongRepo.CheckSecrets(ctx)
			if err == nil {
				t.Fatal("mismatched key/version passed compatibility check")
			}
			for _, sensitive := range []string{sourceKey, "events.example.test", "credential-that-must-not-leak"} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("compatibility error leaked %q: %v", sensitive, err)
				}
			}
			if !strings.Contains(err.Error(), "restore the previous") || !strings.Contains(err.Error(), "re-enter credentials") {
				t.Fatalf("compatibility error lacks recovery guidance: %v", err)
			}
		})
	}
}

func TestSuccessfulEmptyGenericSyncLocksExternalIDMapping(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	codec, err := sourceconfig.NewSecretCodec([]byte("0123456789abcdef0123456789abcdef"), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	sourceKey := "generic:empty-success-" + uuid.NewString()
	source, err := sources.Create(ctx, secretCompatibilityInput(sourceKey, ""))
	if err != nil {
		t.Fatal(err)
	}
	cityID := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'Europe/Moscow',55.75,37.61)`, cityID, "empty-sync-"+cityID.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM provider_sync_runs WHERE city_id=$1`, cityID)
		_, _ = db.Exec(ctx, `DELETE FROM event_sources WHERE id=$1`, source.ID)
		_, _ = db.Exec(ctx, `DELETE FROM cities WHERE id=$1`, cityID)
	})

	store := providers.NewRepository(db)
	now := time.Now().UTC()
	runID, err := store.BeginSyncRun(ctx, providers.SyncRunStart{
		Provider: sourceKey, CityID: cityID,
		WindowStart: now, WindowEnd: now, UpsertOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := providers.FinalizeSyncRun(ctx, store, runID, providers.ImportStats{}, nil); err != nil {
		t.Fatal(err)
	}
	var mappingLocked bool
	if err := db.QueryRow(ctx, `SELECT mapping_locked FROM event_sources WHERE id=$1`, source.ID).Scan(&mappingLocked); err != nil {
		t.Fatal(err)
	}
	if !mappingLocked {
		t.Fatal("successful empty import did not lock external_id mapping")
	}
}

func secretCompatibilityInput(sourceKey, secret string) sourceconfig.Input {
	authType := "none"
	if secret != "" {
		authType = "bearer"
	}
	return sourceconfig.Input{
		SourceKey: sourceKey, Name: "Credential compatibility", Enabled: true,
		EndpointURL: "https://events.example.test/api", AuthType: authType, Secret: secret,
		QueryParams: map[string]string{}, Pagination: json.RawMessage(`{"mode":"none"}`),
		Mapping:   json.RawMessage(`{}`),
		Defaults:  sourceconfig.Defaults{Category: "other", Timezone: "Europe/Moscow", Currency: "RUB", Status: "published"},
		PriceUnit: "major",
	}
}
