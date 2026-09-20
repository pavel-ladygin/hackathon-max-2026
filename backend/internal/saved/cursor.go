package saved

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const maxCursorLength = 512

type cursorCodec struct{ key []byte }
type cursorPayload struct {
	Version int    `json:"v"`
	UserID  string `json:"u"`
	Tab     Tab    `json:"t"`
	At      string `json:"a"`
	ID      string `json:"i"`
}

func newCursorCodec(key []byte) (*cursorCodec, error) {
	if len(key) < 16 {
		return nil, errors.New("saved cursor key must be at least 16 bytes")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("max-together/saved-cursor/v1"))
	return &cursorCodec{key: mac.Sum(nil)}, nil
}
func (c *cursorCodec) Encode(userID uuid.UUID, tab Tab, cursor Cursor) (string, error) {
	if userID == uuid.Nil || (tab != TabSaved && tab != TabMatches) || cursor.ID == uuid.Nil || cursor.At.IsZero() {
		return "", ErrInvalid
	}
	payload, err := json.Marshal(cursorPayload{Version: 1, UserID: userID.String(), Tab: tab, At: cursor.At.UTC().Format(time.RFC3339Nano), ID: cursor.ID.String()})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (c *cursorCodec) Decode(userID uuid.UUID, tab Tab, value string) (Cursor, error) {
	if len(value) == 0 || len(value) > maxCursorLength {
		return Cursor{}, ErrInvalid
	}
	var left, right string
	for i := range value {
		if value[i] == '.' {
			left, right = value[:i], value[i+1:]
			break
		}
	}
	if left == "" || right == "" {
		return Cursor{}, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(left)
	if err != nil {
		return Cursor{}, ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(right)
	if err != nil {
		return Cursor{}, ErrInvalid
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return Cursor{}, ErrInvalid
	}
	var decoded cursorPayload
	if json.Unmarshal(payload, &decoded) != nil || decoded.Version != 1 || decoded.UserID != userID.String() || decoded.Tab != tab {
		return Cursor{}, ErrInvalid
	}
	at, err := time.Parse(time.RFC3339Nano, decoded.At)
	id, parseErr := uuid.Parse(decoded.ID)
	if err != nil || parseErr != nil || id == uuid.Nil || at.IsZero() {
		return Cursor{}, ErrInvalid
	}
	return Cursor{At: at, ID: id}, nil
}
