package behavior

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

var ErrInvalid = errors.New("invalid behavior batch")

type ClientEvent struct {
	ClientEventID string
	Type          string
	OccurredAt    pgtype.Timestamptz
	EventID       pgtype.UUID
	RoomID        pgtype.UUID
	Surface       pgtype.Text
	Position      pgtype.Int4
	RequestID     pgtype.Text
}
type Result struct{ Accepted, Duplicates, Rejected int }
type Service struct{ db *store.Pool }

func NewService(db *store.Pool) (*Service, error) {
	if db == nil {
		return nil, errors.New("behavior service database is required")
	}
	return &Service{db: db}, nil
}

// Ingest accepts only a fully validated batch. This keeps canonical validation
// failures atomic: malformed input returns 400 and writes no partial signals.
func (s *Service) Ingest(ctx context.Context, userID uuid.UUID, events []ClientEvent) (Result, error) {
	if userID == uuid.Nil || len(events) < 1 || len(events) > 100 {
		return Result{}, ErrInvalid
	}
	result := Result{}
	err := s.db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := platform.New(tx)
		for _, event := range events {
			if event.EventID.Valid {
				var exists bool
				if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM events WHERE id = $1)", uuid.UUID(event.EventID.Bytes)).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return ErrInvalid
				}
			}
			rows, err := q.InsertClientBehaviorEvent(ctx, platform.InsertClientBehaviorEventParams{ID: uuid.New(), UserID: userID, Type: event.Type, EventID: event.EventID, RoomID: event.RoomID, Surface: event.Surface, Position: event.Position, RequestID: event.RequestID, ClientEventID: pgtype.Text{String: event.ClientEventID, Valid: true}, OccurredAt: event.OccurredAt})
			if err != nil {
				return err
			}
			if rows == 0 {
				result.Duplicates++
			} else {
				result.Accepted++
			}
		}
		return nil
	})
	return result, err
}
