package sourceconfig

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSecretCodecBindsCiphertextToSourceAndKeyVersion(t *testing.T) {
	codec, err := NewSecretCodec(bytes.Repeat([]byte{0x42}, 32), 3)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	ciphertext, err := codec.Seal(id, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := codec.Open(id, ciphertext, 3)
	if err != nil || plain != "private-token" {
		t.Fatalf("open = %q, %v", plain, err)
	}
	if _, err := codec.Open(uuid.New(), ciphertext, 3); err == nil {
		t.Fatal("ciphertext was accepted for a different source")
	}
	if _, err := codec.Open(id, ciphertext, 2); err == nil {
		t.Fatal("ciphertext was accepted with a different key version")
	}
	ciphertext[0] ^= 1
	if _, err := codec.Open(id, ciphertext, 3); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestSourceJSONNeverIncludesPlaintextSecret(t *testing.T) {
	encoded, err := json.Marshal(Source{SourceKey: "generic:sample", SecretConfigured: true, secretCiphertext: []byte("cipher"), keyVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["secret"]; exists {
		t.Fatalf("serialized source leaked secret: %s", encoded)
	}
	if body["secret_configured"] != true {
		t.Fatalf("secret configured flag missing: %s", encoded)
	}
}

func TestSourceJSONOmitsLegacyTransformConfig(t *testing.T) {
	encoded, err := json.Marshal(Source{})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["transform_config"]; exists {
		t.Fatalf("legacy transform_config leaked into source response: %s", encoded)
	}
}

func TestInputRejectsNonemptyLegacyTransformConfig(t *testing.T) {
	input := Input{
		SourceKey: "generic:legacy-transform", Name: "Legacy transform", EndpointURL: "https://events.example.test",
		AuthType: "none", Defaults: Defaults{Category: "other", Timezone: "Europe/Moscow", Currency: "RUB", Status: "published"},
		PriceUnit: "major", TransformConfig: json.RawMessage(`{}`),
	}
	if err := validateInput(input, true); err != nil {
		t.Fatalf("empty transform_config should remain accepted: %v", err)
	}
	input.TransformConfig = json.RawMessage(`{"title":"strip_html"}`)
	if err := validateInput(input, true); err == nil {
		t.Fatal("nonempty transform_config should be rejected")
	}
}

func TestSecretCodecRequiresValidKey(t *testing.T) {
	for _, test := range []struct {
		key     []byte
		version int16
	}{{[]byte("short"), 1}, {bytes.Repeat([]byte{1}, 32), 0}} {
		if _, err := NewSecretCodec(test.key, test.version); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}
