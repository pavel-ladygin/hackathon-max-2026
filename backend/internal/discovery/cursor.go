package discovery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidCursor = errors.New("invalid discovery cursor")

const maxCursorLength = 512

type cursorPayload struct {
	Version    int    `json:"v"`
	StartsAt   string `json:"s"`
	EventID    string `json:"e"`
	FilterHash string `json:"f"`
}

// CursorCodec signs cursors so callers cannot choose an arbitrary key.
type CursorCodec struct{ key []byte }

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 16 {
		return nil, errors.New("discovery cursor key must be at least 16 bytes")
	}
	// Derive a domain-specific key so the server master key is never reused
	// directly across invite encryption and discovery cursor authentication.
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("max-together/discovery-cursor/v1"))
	return &CursorCodec{key: mac.Sum(nil)}, nil
}

func (c *CursorCodec) Encode(cursor Cursor) (string, error) {
	if cursor.StartsAt.IsZero() || cursor.EventID == uuid.Nil || cursor.FilterHash == "" {
		return "", fmt.Errorf("%w: incomplete key", ErrInvalidCursor)
	}
	payload, err := json.Marshal(cursorPayload{Version: 1, StartsAt: cursor.StartsAt.UTC().Format(time.RFC3339Nano), EventID: cursor.EventID.String(), FilterHash: cursor.FilterHash})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (c *CursorCodec) Decode(value, expectedFilterHash string) (Cursor, error) {
	var zero Cursor
	if len(value) == 0 || len(value) > maxCursorLength {
		return zero, ErrInvalidCursor
	}
	parts := splitCursor(value)
	if len(parts) != 2 {
		return zero, ErrInvalidCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return zero, ErrInvalidCursor
	}
	var decoded cursorPayload
	if json.Unmarshal(payload, &decoded) != nil || decoded.Version != 1 || decoded.FilterHash != expectedFilterHash {
		return zero, ErrInvalidCursor
	}
	startsAt, err := time.Parse(time.RFC3339Nano, decoded.StartsAt)
	if err != nil {
		return zero, ErrInvalidCursor
	}
	eventID, err := uuid.Parse(decoded.EventID)
	if err != nil || eventID == uuid.Nil || startsAt.IsZero() {
		return zero, ErrInvalidCursor
	}
	return Cursor{StartsAt: startsAt, EventID: eventID, FilterHash: decoded.FilterHash}, nil
}

func splitCursor(value string) []string {
	for i := range value {
		if value[i] == '.' {
			return []string{value[:i], value[i+1:]}
		}
	}
	return nil
}
