package rooms

import "errors"

var (
	ErrValidation          = errors.New("invalid create room request")
	ErrUnauthenticated     = errors.New("unauthenticated")
	ErrActiveRoomExists    = errors.New("active room exists")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrCreateUnavailable   = errors.New("create room dependencies unavailable")
	ErrInviteNotFound      = errors.New("invite not found")
	ErrInviteExpired       = errors.New("invite expired")
	ErrRoomFull            = errors.New("room full")
)
