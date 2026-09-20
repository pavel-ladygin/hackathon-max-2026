package rooms

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// Service owns room transaction boundaries and the create-room operation.
type Service struct {
	pool     *store.Pool
	recorder contracts.BehaviorRecorder
	invites  *InviteCodec
}

func NewService(pool *store.Pool) *Service { return &Service{pool: pool} }

// NewCreateService constructs the production create-room service. Creation is
// intentionally unavailable without behavior and response-encryption support.
func NewCreateService(pool *store.Pool, recorder contracts.BehaviorRecorder, invites *InviteCodec) (*Service, error) {
	if pool == nil || recorder == nil || invites == nil {
		return nil, errors.New("rooms create service dependencies are required")
	}
	return &Service{pool: pool, recorder: recorder, invites: invites}, nil
}

// WithTx runs fn at READ COMMITTED and gives it a transaction-bound repository.
func (s *Service) WithTx(ctx context.Context, fn func(*Repository) error) error {
	return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(NewRepository(tx))
	})
}
