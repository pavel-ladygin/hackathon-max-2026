package rooms

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

const roomBuildTxAttempts = 3

// Service owns room transaction boundaries and the create-room operation.
type Service struct {
	pool         *store.Pool
	recorder     contracts.BehaviorRecorder
	invites      *InviteCodec
	builder      contracts.PoolBuilder
	availability contracts.EventAvailability
	eventsCursor *RoomEventsCursorCodec
}

// EnableRoomEvents installs the read-only B7 dependencies on a fully
// constructed room service.
func (s *Service) EnableRoomEvents(availability contracts.EventAvailability, codec *RoomEventsCursorCodec) error {
	if availability == nil || codec == nil {
		return errors.New("room events dependencies are required")
	}
	s.availability = availability
	s.eventsCursor = codec
	return nil
}

func NewService(pool *store.Pool, builders ...contracts.PoolBuilder) *Service {
	var builder contracts.PoolBuilder
	if len(builders) > 0 {
		builder = builders[0]
	}
	return &Service{pool: pool, builder: builder}
}

// NewCreateService constructs the production create-room service. Creation is
// intentionally unavailable without behavior and response-encryption support.
func NewCreateService(pool *store.Pool, recorder contracts.BehaviorRecorder, invites *InviteCodec, builders ...contracts.PoolBuilder) (*Service, error) {
	if pool == nil || recorder == nil || invites == nil || len(builders) != 1 || builders[0] == nil {
		return nil, errors.New("rooms create service dependencies are required")
	}
	return &Service{pool: pool, recorder: recorder, invites: invites, builder: builders[0]}, nil
}

// WithTx runs fn at READ COMMITTED and gives it a transaction-bound repository.
func (s *Service) WithTx(ctx context.Context, fn func(*Repository) error) error {
	return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(NewRepository(tx))
	})
}

// withRoomBuildTx gives the room transition and all catalog reads used by the
// builder one coherent PostgreSQL snapshot. Concurrent room updates can make a
// REPEATABLE READ transaction fail with SQLSTATE 40001; retrying the complete
// deterministic operation is safe because no writes escape the transaction.
func (s *Service) withRoomBuildTx(ctx context.Context, fn func(*Repository) error) error {
	for attempt := 0; attempt < roomBuildTxAttempts; attempt++ {
		err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			return fn(NewRepository(tx))
		})
		if err == nil || !retryableRoomTxError(err) || attempt == roomBuildTxAttempts-1 {
			return err
		}
	}
	return nil
}

func retryableRoomTxError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

// WithReadTx gives multi-query room reads one consistent database snapshot.
func (s *Service) WithReadTx(ctx context.Context, fn func(*Repository) error) error {
	return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return fn(NewRepository(tx))
	})
}
