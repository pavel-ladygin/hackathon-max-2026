package behavior

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// Recorder writes server events through the caller's transaction only.
type Recorder struct{}

var _ contracts.BehaviorRecorder = Recorder{}

func (Recorder) Record(ctx context.Context, db store.DBTX, event contracts.ServerBehaviorEvent) error {
	if !validServerEvent(event) {
		return errors.New("invalid server behavior event")
	}
	_, err := db.Exec(ctx, `INSERT INTO behavior_events
		(id, user_id, origin, type, event_id, room_id, request_id, occurred_at)
		VALUES ($1, $2, 'server', $3, $4, $5, $6, $7)`,
		event.ID, event.UserID, event.Type, event.EventID, event.RoomID,
		event.RequestID, event.OccurredAt)
	return err
}

func validServerEvent(event contracts.ServerBehaviorEvent) bool {
	if event.ID == uuid.Nil || event.UserID == uuid.Nil || event.OccurredAt.IsZero() ||
		len(event.RequestID) > 128 || strings.TrimSpace(event.Type) != event.Type {
		return false
	}
	hasEvent := event.EventID != nil && *event.EventID != uuid.Nil
	hasRoom := event.RoomID != nil && *event.RoomID != uuid.Nil
	switch event.Type {
	case "room_create", "room_join", "intent_submit":
		return hasRoom
	case "like", "dislike", "match":
		return hasEvent && hasRoom
	case "save", "unsave", "ticket_click":
		return hasEvent
	default:
		return false
	}
}
