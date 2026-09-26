package behavior

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
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
	SessionID     pgtype.UUID
	Platform      pgtype.Text
	AppVersion    pgtype.Text
	EntryPoint    pgtype.Text
	Properties    []byte
	EventVersion  int32
}
type Result struct{ Accepted, Duplicates, Rejected int }
type Service struct{ db *store.Pool }

var _ contracts.BehavioralAffinityLoader = (*Service)(nil)

func NewService(db *store.Pool) (*Service, error) {
	if db == nil {
		return nil, errors.New("behavior service database is required")
	}
	return &Service{db: db}, nil
}

// RunRetentionPrune calls the migration-owned pruning function at startup and
// every day. Each call is bounded and failures never affect API availability.
func RunRetentionPrune(ctx context.Context, db *store.Pool, logger *slog.Logger) {
	if db == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	prune := func() {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var deleted int64
		if err := db.QueryRow(callCtx, "SELECT analytics_prune_behavior_events()").Scan(&deleted); err != nil {
			logger.Error("analytics retention prune failed", "error", err)
			return
		}
		if deleted > 0 {
			logger.Info("analytics retention prune completed", "deleted_events", deleted)
		}
	}
	prune()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

// LoadBehavioralCategoryCounts returns deterministic per-user aggregates from
// canonical room votes. behavior_events mirrors these votes and is deliberately
// not counted a second time.
func (s *Service) LoadBehavioralCategoryCounts(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]contracts.BehavioralCategoryCount, error) {
	result := make(map[uuid.UUID][]contracts.BehavioralCategoryCount, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT rv.user_id, ec.category_slug,
		       count(*) FILTER (WHERE rv.vote = 'like') AS likes,
		       count(*) FILTER (WHERE rv.vote = 'dislike') AS dislikes
		FROM room_votes rv
		JOIN event_categories ec ON ec.event_id = rv.event_id AND ec.is_primary
		WHERE rv.user_id = ANY($1::uuid[])
		GROUP BY rv.user_id, ec.category_slug
		ORDER BY rv.user_id, ec.category_slug`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID uuid.UUID
		var count contracts.BehavioralCategoryCount
		if err := rows.Scan(&userID, &count.CategorySlug, &count.Likes, &count.Dislikes); err != nil {
			return nil, err
		}
		result[userID] = append(result[userID], count)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Ingest accepts only a fully validated batch. This keeps canonical validation
// failures atomic: malformed input returns 400 and writes no partial signals.
func (s *Service) Ingest(ctx context.Context, userID uuid.UUID, events []ClientEvent) (Result, error) {
	if userID == uuid.Nil || len(events) < 1 || len(events) > 100 {
		return Result{}, ErrInvalid
	}
	result := Result{}
	err := s.db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		for _, event := range events {
			properties := event.Properties
			if len(properties) == 0 {
				properties = []byte(`{}`)
			}
			eventVersion := event.EventVersion
			if eventVersion == 0 {
				eventVersion = 1
			}
			var propertyFields map[string]json.RawMessage
			if eventVersion != 1 || json.Unmarshal(properties, &propertyFields) != nil || propertyFields == nil {
				return ErrInvalid
			}
			if event.EventID.Valid {
				var exists bool
				if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM events WHERE id = $1)", uuid.UUID(event.EventID.Bytes)).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return ErrInvalid
				}
			}
			// Use the transaction directly because the event envelope has fields
			// newer than the generated sqlc query. The unique client event ID and
			// the canonical deduplication key make retries idempotent.
			rows, err := tx.Exec(ctx, `INSERT INTO behavior_events
				(id, user_id, origin, type, event_id, room_id, surface, position,
				 request_id, client_event_id, occurred_at, event_version, session_id,
				 platform, app_version, entry_point, properties, deduplication_key)
				VALUES ($1, $2, 'client', $3, $4, $5, $6, $7, $8, $9, $10, $11,
				 $12, $13, $14, $15, $16::jsonb, $9)
				ON CONFLICT DO NOTHING`, uuid.New(), userID, event.Type, event.EventID,
				event.RoomID, event.Surface, event.Position, event.RequestID,
				pgtype.Text{String: event.ClientEventID, Valid: true}, event.OccurredAt,
				eventVersion, event.SessionID, event.Platform, event.AppVersion, event.EntryPoint, string(properties))
			if err != nil {
				return err
			}
			if rows.RowsAffected() == 0 {
				result.Duplicates++
			} else {
				result.Accepted++
			}
		}
		return nil
	})
	return result, err
}
