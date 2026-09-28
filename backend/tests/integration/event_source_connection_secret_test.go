package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
)

func TestEventSourceSecretCannotMoveToChangedConnection(t *testing.T) {
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
	input := sourceconfig.Input{
		SourceKey: "generic:" + uuid.NewString(), Name: "Connection secret test", Enabled: true,
		EndpointURL: "https://events.example.test/api", AuthType: "api_key_header", AuthName: "X-Events-Key", Secret: "original-secret",
		QueryParams: map[string]string{"locale": "ru"},
		Pagination:  json.RawMessage(`{"mode":"none"}`),
		Mapping:     json.RawMessage(`{"external_id":{"path":"id"}}`),
		Defaults:    sourceconfig.Defaults{Category: "other", Timezone: "Europe/Moscow", Currency: "RUB", Status: "published"},
		PriceUnit:   "major",
	}
	source, err := repo.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(ctx, `DELETE FROM event_sources WHERE id=$1`, source.ID) })

	// Non-connection edits may continue using the saved secret.
	unchanged := input
	unchanged.Secret = ""
	unchanged.Name = "Renamed source"
	unchanged.Enabled = false
	if _, err := repo.Update(ctx, source.ID, unchanged); err != nil {
		t.Fatalf("unchanged connection rejected: %v", err)
	}

	mutations := []struct {
		name   string
		change func(*sourceconfig.Input)
	}{
		{"endpoint", func(in *sourceconfig.Input) { in.EndpointURL = "https://attacker.example.test/api" }},
		{"auth type", func(in *sourceconfig.Input) { in.AuthType = "api_key_query"; in.AuthName = "api_key" }},
		{"auth name", func(in *sourceconfig.Input) { in.AuthName = "X-Other-Key" }},
		{"query params", func(in *sourceconfig.Input) { in.QueryParams = map[string]string{"locale": "en"} }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := unchanged
			changed.Secret = ""
			mutation.change(&changed)
			if _, err := repo.Update(ctx, source.ID, changed); !errors.Is(err, sourceconfig.ErrSecretRequiredForConnectionChange) {
				t.Fatalf("Update error = %v, want ErrSecretRequiredForConnectionChange", err)
			}
		})
	}

	// An explicit replacement secret authorizes the changed destination.
	replacement := unchanged
	replacement.EndpointURL = "https://new-events.example.test/api"
	replacement.Secret = "replacement-secret"
	if _, err := repo.Update(ctx, source.ID, replacement); err != nil {
		t.Fatalf("explicit secret replacement rejected: %v", err)
	}
	_, secret, err := repo.GetForExecution(ctx, source.ID)
	if err != nil || secret != "replacement-secret" {
		t.Fatalf("replacement secret = %q, err %v", secret, err)
	}

	// Disabling authentication explicitly clears the stored credential.
	noAuth := replacement
	noAuth.Secret = ""
	noAuth.AuthType = "none"
	noAuth.AuthName = ""
	noAuth.EndpointURL = "https://public-events.example.test/api"
	if _, err := repo.Update(ctx, source.ID, noAuth); err != nil {
		t.Fatalf("clearing authentication rejected: %v", err)
	}
	updated, secret, err := repo.GetForExecution(ctx, source.ID)
	if err != nil || secret != "" || updated.SecretConfigured {
		t.Fatalf("secret after disabling auth = %q configured=%t err=%v", secret, updated.SecretConfigured, err)
	}
}
