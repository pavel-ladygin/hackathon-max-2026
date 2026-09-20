package behavior

import (
	"context"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// Recorder writes server events through the caller's transaction only.
type Recorder struct{}

var _ contracts.BehaviorRecorder = Recorder{}

func (Recorder) Record(ctx context.Context, db store.DBTX, event contracts.ServerBehaviorEvent) error {
	_, err := db.Exec(ctx, `INSERT INTO behavior_events
		(id, user_id, origin, type, event_id, room_id, request_id, occurred_at)
		VALUES ($1, $2, 'server', $3, $4, $5, $6, $7)`,
		event.ID, event.UserID, event.Type, event.EventID, event.RoomID,
		event.RequestID, event.OccurredAt)
	return err
}
