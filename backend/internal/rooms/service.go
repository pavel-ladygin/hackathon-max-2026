package rooms

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// Service supplies the room transaction boundary. Lifecycle operations are
// deliberately not implemented by this foundation.
type Service struct {
	pool *store.Pool
}

func NewService(pool *store.Pool) *Service { return &Service{pool: pool} }

// WithTx runs fn at READ COMMITTED and gives it a transaction-bound repository.
func (s *Service) WithTx(ctx context.Context, fn func(*Repository) error) error {
	return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(NewRepository(tx))
	})
}
