package rooms

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

const expiryCleanupBatch = 100

// CleanupExpired retires expired rooms without deleting their audit history.
func (s *Service) CleanupExpired(ctx context.Context) (int, error) {
	cleaned := 0
	for {
		if err := ctx.Err(); err != nil {
			return cleaned, err
		}
		batchCleaned := 0
		err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			repo := NewRepository(tx)
			now, err := repo.Queries.ClockNow(ctx)
			if err != nil {
				return err
			}
			rooms, err := repo.Queries.LockExpiredRoomsForCleanup(ctx, roomsql.LockExpiredRoomsForCleanupParams{ExpiresAt: now, Limit: expiryCleanupBatch})
			if err != nil {
				return err
			}
			for _, room := range rooms {
				if err := ctx.Err(); err != nil {
					return err
				}
				if _, err := repo.Queries.RetireRoomMemberships(ctx, room.ID); err != nil {
					return err
				}
				if _, err := repo.Queries.ClearRoomIntentCoordinates(ctx, room.ID); err != nil {
					return err
				}
				if _, err := repo.Queries.ExpireRoomInvites(ctx, roomsql.ExpireRoomInvitesParams{RoomID: room.ID, ExpiresAt: pgtype.Timestamptz{Time: now.Time, Valid: true}}); err != nil {
					return err
				}
			}
			// Expired invite rows retain encrypted material only for 24 hours.
			// This sweep is intentionally independent of active membership so
			// terminal and previously cleaned rooms are included.
			if _, err := repo.Queries.DeleteInviteSecretsExpiredBefore(ctx, pgtype.Timestamptz{Time: now.Time.Add(-24 * time.Hour), Valid: true}); err != nil {
				return err
			}
			batchCleaned = len(rooms)
			return nil
		})
		if err != nil {
			return cleaned, err
		}
		cleaned += batchCleaned
		if batchCleaned == 0 {
			return cleaned, nil
		}
	}
}

// RunExpiryCleanup performs one startup pass and then periodic bounded passes.
func (s *Service) RunExpiryCleanup(ctx context.Context, interval time.Duration, onError func(error)) {
	run := func() {
		if _, err := s.CleanupExpired(ctx); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
