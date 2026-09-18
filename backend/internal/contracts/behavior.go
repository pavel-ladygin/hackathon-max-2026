package contracts

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// BehaviorRecorder writes a server-authoritative event using the caller's
// executor. Backend B MUST pass its current pgx.Tx so the action and behavior
// record commit or roll back together. Implementations must not begin or commit
// another transaction or bypass the supplied executor through their own pool.
type BehaviorRecorder interface {
	Record(ctx context.Context, db store.DBTX, event ServerBehaviorEvent) error
}

// ServerBehaviorEvent excludes client-supplied arbitrary metadata and PII.
// Origin is always "server" and is assigned by the recorder implementation.
type ServerBehaviorEvent struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Type       string // room_create, room_join, intent_submit, like, dislike, match, save, unsave, ticket_click.
	EventID    *uuid.UUID
	RoomID     *uuid.UUID
	RequestID  string
	OccurredAt time.Time
}
