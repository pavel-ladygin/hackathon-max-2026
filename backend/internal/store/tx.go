package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const rollbackTimeout = 5 * time.Second

// InTx runs fn inside a transaction. It commits only when fn returns nil.
// Rollback uses an independent bounded context so cancellation cannot strand a transaction.
func (p *Pool) InTx(ctx context.Context, options pgx.TxOptions, fn func(pgx.Tx) error) (err error) {
	tx, err := p.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			rollback(tx)
			panic(recovered)
		}
		if err != nil {
			rollback(tx)
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	_ = tx.Rollback(ctx)
}
