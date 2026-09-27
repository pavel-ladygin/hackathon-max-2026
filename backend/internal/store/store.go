// Package store contains PostgreSQL access primitives shared by sqlc packages.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pingTimeout = 5 * time.Second

// DBTX is the pgx query surface used by sqlc-generated query code.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

var (
	_ DBTX = (*pgxpool.Pool)(nil)
	_ DBTX = (pgx.Tx)(nil)
)

// Pool owns the PostgreSQL connection pool used by application services.
type Pool struct {
	*pgxpool.Pool
}

// Open creates a PostgreSQL pool and verifies that the database is reachable.
func Open(ctx context.Context, databaseURL string) (*Pool, error) {
	config, err := applicationPoolConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres pool config: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	checkCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(checkCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Pool{Pool: pool}, nil
}

func applicationPoolConfig(databaseURL string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	// The analytics dashboard query causes PostgreSQL to spend far longer
	// compiling JIT functions than executing the query. Disable JIT only for
	// application connections; migration and administrative sessions retain
	// the database's configured default.
	config.ConnConfig.RuntimeParams["jit"] = "off"
	return config, nil
}

// Ping verifies database connectivity with a short bounded timeout.
func (p *Pool) Ping(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	return p.Pool.Ping(checkCtx)
}
