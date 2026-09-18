// Package matching owns transaction-scoped vote and match persistence access.
package matching

import (
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

// Repository shares the room sqlc package and is bound to the caller's room
// transaction. It intentionally does not start a second transaction.
type Repository struct {
	Queries *roomsql.Queries
	tx      pgx.Tx
}

func NewRepository(tx pgx.Tx) *Repository {
	return &Repository{Queries: roomsql.New(tx), tx: tx}
}

// DBTX returns the same transaction used by Queries.
func (r *Repository) DBTX() store.DBTX { return r.tx }
