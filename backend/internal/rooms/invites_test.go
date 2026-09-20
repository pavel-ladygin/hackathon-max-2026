package rooms

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testInviteCodec(t *testing.T) *InviteCodec {
	t.Helper()
	codec, err := NewInviteCodec(bytes.Repeat([]byte{0x42}, 32), 7,
		"https://app.test/invite/{token}", "https://max.test/bot?startapp={token}")
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func TestInviteEncryptionAndContextBinding(t *testing.T) {
	codec := testInviteCodec(t)
	room := uuid.New()
	expires := time.Now().Add(48 * time.Hour)
	one, err := codec.New(room, expires)
	if err != nil {
		t.Fatal(err)
	}
	two, err := codec.New(room, expires)
	if err != nil {
		t.Fatal(err)
	}
	random, err := base64.RawURLEncoding.Strict().DecodeString(one.Token)
	if err != nil || len(random) != 32 || one.Token == two.Token {
		t.Fatal("invitations must have independent 256-bit base64url tokens")
	}
	hash := sha256.Sum256([]byte(one.Token))
	if !bytes.Equal(hash[:], one.Hash) || bytes.Contains(one.Ciphertext, []byte(one.Token)) {
		t.Fatal("incorrect hash or plaintext ciphertext")
	}
	if !strings.HasSuffix(one.URL, one.Token) || !strings.HasSuffix(one.MaxDeepLink, one.Token) || !one.ExpiresAt.Equal(expires) {
		t.Fatal("incorrect invitation material")
	}
	if plain, err := codec.OpenToken(room, one.Ciphertext, one.KeyVersion); err != nil || plain != one.Token {
		t.Fatalf("recover invite: %v", err)
	}
	tampered := bytes.Clone(one.Ciphertext)
	tampered[len(tampered)-1] ^= 1
	for name, tc := range map[string]struct {
		room    uuid.UUID
		data    []byte
		version int16
	}{
		"different room":    {uuid.New(), one.Ciphertext, 7},
		"different version": {room, one.Ciphertext, 8},
		"tampered":          {room, tampered, 7},
		"truncated":         {room, one.Ciphertext[:5], 7},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.OpenToken(tc.room, tc.data, tc.version); !errors.Is(err, errInviteCiphertext) {
				t.Fatalf("got %v; want safe ciphertext error", err)
			}
		})
	}
}

func TestCreateResponseEncryptionAndContextBinding(t *testing.T) {
	codec := testInviteCodec(t)
	user := uuid.New()
	const key = "idempotency-key"
	response := []byte(`{"invite":{"token":"private-token"},"room":{"name":"private name"}}`)
	envelope, err := codec.SealResponse(user, key, response)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(envelope) || bytes.Contains(envelope, []byte("private-token")) || bytes.Contains(envelope, []byte("private name")) {
		t.Fatal("replay envelope must be JSON containing no plaintext response")
	}
	if got, err := codec.OpenResponse(user, key, envelope); err != nil || !bytes.Equal(got, response) {
		t.Fatalf("replay decrypt: %v", err)
	}
	for name, tc := range map[string]struct {
		user     uuid.UUID
		key      string
		envelope []byte
	}{
		"another user":    {uuid.New(), key, envelope},
		"another request": {user, "different-key", envelope},
		"malformed":       {user, key, []byte(`{"ciphertext":"invalid"}`)},
		"null":            {user, key, []byte(`null`)},
		"wrong version":   {user, key, bytes.Replace(envelope, []byte(`"key_version":7`), []byte(`"key_version":8`), 1)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.OpenResponse(tc.user, tc.key, tc.envelope); !errors.Is(err, errInviteCiphertext) {
				t.Fatalf("got %v; want safe ciphertext error", err)
			}
		})
	}
}

func TestInviteConfigurationRejectsUnsafeOrIncompleteValues(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	for _, template := range []string{
		"", "https://app.test/invite", "https://{token}.test/", "https://user:pass@app.test/{token}",
		"http://app.test/{token}", "javascript:{token}", "https://app.test/{token}?token={token}",
	} {
		if _, err := NewInviteCodec(key, 1, template, "https://max.test/{token}"); err == nil {
			t.Errorf("accepted invalid URL template %q", template)
		}
	}
	if _, err := NewInviteCodec(key[:16], 1, "https://app.test/{token}", "https://max.test/{token}"); err == nil {
		t.Error("accepted short key")
	}
	if _, err := NewInviteCodec(key, 0, "https://app.test/{token}", "https://max.test/{token}"); err == nil {
		t.Error("accepted zero version")
	}
}
