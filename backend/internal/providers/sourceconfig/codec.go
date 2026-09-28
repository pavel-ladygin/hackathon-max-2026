// Package sourceconfig stores configurable provider sources and protects their credentials.
package sourceconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"strconv"

	"github.com/google/uuid"
)

// SecretCodec uses a domain-separated key derived from the application's
// master key. Ciphertexts are bound to a source ID and key version.
type SecretCodec struct {
	aead    cipher.AEAD
	version int16
}

func NewSecretCodec(masterKey []byte, version int16) (*SecretCodec, error) {
	if len(masterKey) < 32 || version < 1 {
		return nil, errors.New("provider secret encryption requires a 32-byte key and positive version")
	}
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte("max-together/provider-secrets/v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, errors.New("provider secret encryption unavailable")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("provider secret encryption unavailable")
	}
	return &SecretCodec{aead: aead, version: version}, nil
}

func (c *SecretCodec) Seal(sourceID uuid.UUID, plaintext string) ([]byte, error) {
	if c == nil || c.aead == nil || sourceID == uuid.Nil || plaintext == "" {
		return nil, errors.New("provider secret is invalid")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("provider secret encryption unavailable")
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), c.aad(sourceID)), nil
}

func (c *SecretCodec) Open(sourceID uuid.UUID, ciphertext []byte, version int16) (string, error) {
	if c == nil || c.aead == nil || sourceID == uuid.Nil || version != c.version || len(ciphertext) < c.aead.NonceSize()+c.aead.Overhead() {
		return "", errors.New("provider secret decryption failed")
	}
	nonce, sealed := ciphertext[:c.aead.NonceSize()], ciphertext[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, sealed, c.aad(sourceID))
	if err != nil {
		return "", errors.New("provider secret decryption failed")
	}
	return string(plain), nil
}

func (c *SecretCodec) aad(sourceID uuid.UUID) []byte {
	return []byte("event-source/" + strconv.Itoa(int(c.version)) + "/" + sourceID.String())
}
