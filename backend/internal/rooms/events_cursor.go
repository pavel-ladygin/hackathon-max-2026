package rooms

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

var ErrInvalidRoomEventsCursor = errors.New("invalid room events cursor")

const maxRoomEventsCursorLength = 512

type roomEventsCursor struct {
	RoomID      uuid.UUID
	PoolVersion int32
	UserID      uuid.UUID
	Position    int32
}

type roomEventsCursorPayload struct {
	Version     int    `json:"v"`
	RoomID      string `json:"r"`
	PoolVersion int32  `json:"p"`
	UserID      string `json:"u"`
	Position    int32  `json:"o"`
}

type RoomEventsCursorCodec struct{ key []byte }

func NewRoomEventsCursorCodec(key []byte) (*RoomEventsCursorCodec, error) {
	if len(key) < 16 {
		return nil, errors.New("room events cursor key must be at least 16 bytes")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("max-together/room-events-cursor/v1"))
	return &RoomEventsCursorCodec{key: mac.Sum(nil)}, nil
}

func (c *RoomEventsCursorCodec) Encode(cursor roomEventsCursor) (string, error) {
	if c == nil || cursor.RoomID == uuid.Nil || cursor.UserID == uuid.Nil || cursor.PoolVersion < 1 || cursor.Position < 0 {
		return "", ErrInvalidRoomEventsCursor
	}
	payload, err := json.Marshal(roomEventsCursorPayload{Version: 1, RoomID: cursor.RoomID.String(), PoolVersion: cursor.PoolVersion, UserID: cursor.UserID.String(), Position: cursor.Position})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (c *RoomEventsCursorCodec) Decode(value string, expected roomEventsCursor) (roomEventsCursor, error) {
	var zero roomEventsCursor
	if c == nil || value == "" || len(value) > maxRoomEventsCursorLength {
		return zero, ErrInvalidRoomEventsCursor
	}
	separator := -1
	for i := range value {
		if value[i] == '.' {
			separator = i
			break
		}
	}
	if separator <= 0 || separator == len(value)-1 {
		return zero, ErrInvalidRoomEventsCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(value[:separator])
	if err != nil {
		return zero, ErrInvalidRoomEventsCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(value[separator+1:])
	if err != nil {
		return zero, ErrInvalidRoomEventsCursor
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return zero, ErrInvalidRoomEventsCursor
	}
	var decoded roomEventsCursorPayload
	if json.Unmarshal(payload, &decoded) != nil || decoded.Version != 1 || decoded.PoolVersion < 1 || decoded.Position < 0 {
		return zero, ErrInvalidRoomEventsCursor
	}
	roomID, roomErr := uuid.Parse(decoded.RoomID)
	userID, userErr := uuid.Parse(decoded.UserID)
	if roomErr != nil || userErr != nil || roomID == uuid.Nil || userID == uuid.Nil {
		return zero, ErrInvalidRoomEventsCursor
	}
	result := roomEventsCursor{RoomID: roomID, PoolVersion: decoded.PoolVersion, UserID: userID, Position: decoded.Position}
	if result.RoomID != expected.RoomID || result.PoolVersion != expected.PoolVersion || result.UserID != expected.UserID {
		return zero, ErrInvalidRoomEventsCursor
	}
	return result, nil
}
