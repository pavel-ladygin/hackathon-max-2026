package rooms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var errInviteCiphertext = errors.New("invalid encrypted room data")

// InviteCodec protects invitations and persisted create responses. A response
// contains the token too, so storing it unencrypted would bypass invite secrecy.
// Keep the configured key/version stable for the lifetime of retained invites
// and idempotency records. Unknown key versions fail closed.
type InviteCodec struct {
	aead           cipher.AEAD
	version        int16
	publicTemplate string
	maxTemplate    string
}

type InviteMaterial struct {
	Token       string
	Hash        []byte
	Ciphertext  []byte
	KeyVersion  int16
	URL         string
	MaxDeepLink string
	ExpiresAt   time.Time
}

// NewInviteCodec accepts a 32-byte AES key and deployment-provided URL templates.
// Each template must contain {token} exactly once in its path, query or fragment;
// it cannot control the authority. No deployment hostname or MAX bot is invented.
func NewInviteCodec(key []byte, keyVersion int16, publicURLTemplate, maxDeepLinkTemplate string) (*InviteCodec, error) {
	if len(key) != 32 || keyVersion < 1 {
		return nil, errors.New("invite encryption requires a 32-byte key and positive version")
	}
	if !validInviteTemplate(publicURLTemplate) || !validInviteTemplate(maxDeepLinkTemplate) {
		return nil, errors.New("invite URLs require a valid HTTPS template with one {token} placeholder")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &InviteCodec{aead: aead, version: keyVersion, publicTemplate: publicURLTemplate, maxTemplate: maxDeepLinkTemplate}, nil
}

func validInviteTemplate(template string) bool {
	if strings.Count(template, "{token}") != 1 {
		return false
	}
	u, err := url.Parse(strings.Replace(template, "{token}", "invite-token-placeholder", 1))
	if err != nil || u.Hostname() == "" || u.User != nil || strings.Contains(u.Host, "invite-token-placeholder") {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}

func (c *InviteCodec) New(roomID uuid.UUID, expires time.Time) (InviteMaterial, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return InviteMaterial{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(token))
	ciphertext, err := c.seal([]byte(token), c.inviteAAD(roomID))
	if err != nil {
		return InviteMaterial{}, err
	}
	return InviteMaterial{
		Token: token, Hash: hash[:], Ciphertext: ciphertext, KeyVersion: c.version,
		URL:         strings.Replace(c.publicTemplate, "{token}", token, 1),
		MaxDeepLink: strings.Replace(c.maxTemplate, "{token}", token, 1), ExpiresAt: expires,
	}, nil
}

// OpenToken supports creator re-share using the persisted invite ciphertext.
func (c *InviteCodec) OpenToken(roomID uuid.UUID, ciphertext []byte, keyVersion int16) (string, error) {
	if keyVersion != c.version {
		return "", errInviteCiphertext
	}
	plain, err := c.open(ciphertext, c.inviteAAD(roomID))
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(string(plain))
	if err != nil || len(raw) != 32 {
		return "", errInviteCiphertext
	}
	return string(plain), nil
}

func (c *InviteCodec) Recover(roomID uuid.UUID, ciphertext []byte, keyVersion int16, expiresAt time.Time) (InviteMaterial, error) {
	token, err := c.OpenToken(roomID, ciphertext, keyVersion)
	if err != nil {
		return InviteMaterial{}, err
	}
	hash := sha256.Sum256([]byte(token))
	return InviteMaterial{
		Token: token, Hash: hash[:], Ciphertext: ciphertext, KeyVersion: keyVersion,
		URL: strings.Replace(c.publicTemplate, "{token}", token, 1), MaxDeepLink: strings.Replace(c.maxTemplate, "{token}", token, 1), ExpiresAt: expiresAt,
	}, nil
}

type encryptedCreateResponse struct {
	KeyVersion int16  `json:"key_version"`
	Ciphertext []byte `json:"ciphertext"`
}

func (c *InviteCodec) SealResponse(user uuid.UUID, key string, response []byte) ([]byte, error) {
	ciphertext, err := c.seal(response, c.responseAAD(user, key))
	if err != nil {
		return nil, err
	}
	return json.Marshal(encryptedCreateResponse{KeyVersion: c.version, Ciphertext: ciphertext})
}

func (c *InviteCodec) OpenResponse(user uuid.UUID, key string, envelope []byte) ([]byte, error) {
	var encrypted encryptedCreateResponse
	if err := json.Unmarshal(envelope, &encrypted); err != nil || encrypted.KeyVersion != c.version {
		return nil, errInviteCiphertext
	}
	return c.open(encrypted.Ciphertext, c.responseAAD(user, key))
}

func (c *InviteCodec) SealJoinResponse(user uuid.UUID, key string, response []byte) ([]byte, error) {
	ciphertext, err := c.seal(response, c.joinResponseAAD(user, key))
	if err != nil {
		return nil, err
	}
	return json.Marshal(encryptedCreateResponse{KeyVersion: c.version, Ciphertext: ciphertext})
}

func (c *InviteCodec) OpenJoinResponse(user uuid.UUID, key string, envelope []byte) ([]byte, error) {
	var encrypted encryptedCreateResponse
	if err := json.Unmarshal(envelope, &encrypted); err != nil || encrypted.KeyVersion != c.version {
		return nil, errInviteCiphertext
	}
	return c.open(encrypted.Ciphertext, c.joinResponseAAD(user, key))
}

func (c *InviteCodec) inviteAAD(roomID uuid.UUID) []byte {
	return []byte("rooms/invite/" + strconv.Itoa(int(c.version)) + "/" + roomID.String())
}

func (c *InviteCodec) responseAAD(user uuid.UUID, key string) []byte {
	// Fixed-shape JSON avoids ambiguous concatenation of arbitrary header values.
	aad, _ := json.Marshal([]string{"rooms/create-response", strconv.Itoa(int(c.version)), user.String(), "/api/v1/rooms", key})
	return aad
}

func (c *InviteCodec) joinResponseAAD(user uuid.UUID, key string) []byte {
	aad, _ := json.Marshal([]string{"rooms/join-response", strconv.Itoa(int(c.version)), user.String(), joinRoute, key})
	return aad
}

func (c *InviteCodec) seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plain, aad), nil
}

func (c *InviteCodec) open(encrypted, aad []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(encrypted) < n+c.aead.Overhead() {
		return nil, errInviteCiphertext
	}
	plain, err := c.aead.Open(nil, encrypted[:n], encrypted[n:], aad)
	if err != nil {
		return nil, errInviteCiphertext
	}
	return plain, nil
}
