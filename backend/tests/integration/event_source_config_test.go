package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
)

func TestEventSourceConfigSecretPersistenceAndIdentityLocks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	codec, err := sourceconfig.NewSecretCodec([]byte("0123456789abcdef0123456789abcdef"), 1)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	key := "generic:" + uuid.NewString()
	input := sourceconfig.Input{
		SourceKey: key, Name: "Config test", Enabled: true, EndpointURL: "https://events.example.test/api",
		AuthType: "bearer", Secret: "top-secret", QueryParams: map[string]string{"locale": "ru"},
		Pagination: json.RawMessage(`{"mode":"none"}`), Mapping: json.RawMessage(`{"external_id":{"path":"id"}}`),
		Defaults: sourceconfig.Defaults{Category: "other", Timezone: "Europe/Moscow", Currency: "RUB", Status: "published"}, PriceUnit: "major",
	}
	source, err := repo.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(ctx, `DELETE FROM event_sources WHERE id=$1`, source.ID) })
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "top-secret") {
		t.Fatalf("source response disclosed secret: %s", encoded)
	}
	updated := input
	updated.Secret = ""
	updated.Name = "Updated config"
	if _, err := repo.Update(ctx, source.ID, updated); err != nil {
		t.Fatal(err)
	}
	_, secret, err := repo.GetForExecution(ctx, source.ID)
	if err != nil || secret != "top-secret" {
		t.Fatalf("preserved secret = %q, err %v", secret, err)
	}
	if err := repo.MarkMappingLocked(ctx, key); err != nil {
		t.Fatal(err)
	}
	updated.SourceKey = key + "-changed"
	if _, err := repo.Update(ctx, source.ID, updated); err != sourceconfig.ErrIdentityImmutable {
		t.Fatalf("source key mutation err = %v", err)
	}
	updated.SourceKey = key
	updated.Mapping = json.RawMessage(`{"external_id":{"path":"new_id"}}`)
	if _, err := repo.Update(ctx, source.ID, updated); err != sourceconfig.ErrMappingImmutable {
		t.Fatalf("mapping mutation err = %v", err)
	}
}
