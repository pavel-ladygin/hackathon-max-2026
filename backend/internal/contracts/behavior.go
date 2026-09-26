package contracts

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// BehaviorRecorder writes a server-authoritative event using the caller's
// transaction. Implementations must isolate analytics write failures so they
// cannot abort the surrounding product operation, and must not begin or commit
// another transaction or bypass the supplied executor through their own pool.
type BehaviorRecorder interface {
	Record(ctx context.Context, db store.DBTX, event ServerBehaviorEvent) error
}

// ServerBehaviorEvent excludes client-supplied arbitrary metadata and PII.
// Origin is always "server" and is assigned by the recorder implementation.
type ServerBehaviorEvent struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	Type             string
	EventID          *uuid.UUID
	RoomID           *uuid.UUID
	RequestID        string
	OccurredAt       time.Time
	Properties       json.RawMessage
	DeduplicationKey string
}
