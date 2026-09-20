// Package rooms owns transaction-scoped room persistence orchestration.
package rooms

import (
	"context"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

// Repository is bound to one transaction. Its generated Queries and DBTX
// always use that same executor, including for a behavior recorder.
type Repository struct {
	Queries *roomsql.Queries
	tx      pgx.Tx
}

// NewRepository binds room queries to tx. It must be called only inside the
// owning service transaction.
func NewRepository(tx pgx.Tx) *Repository {
	return &Repository{Queries: roomsql.New(tx), tx: tx}
}

// DBTX returns the transaction backing Queries, so related persistence can
// commit or roll back with room state.
func (r *Repository) DBTX() store.DBTX { return r.tx }

// LockRooms locks each distinct room in UUID byte order. Membership-changing
// flows lock the user first; room-local flows need only their room lock. READ
// COMMITTED makes reads after these locks observe current rows.
func (r *Repository) LockRooms(ctx context.Context, ids ...uuid.UUID) ([]roomsql.Room, error) {
	unique := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		unique[id] = struct{}{}
	}
	ordered := make([]uuid.UUID, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return string(ordered[i][:]) < string(ordered[j][:]) })

	locked := make([]roomsql.Room, 0, len(ordered))
	for _, id := range ordered {
		room, err := r.Queries.LockRoom(ctx, id)
		if err != nil {
			return nil, err
		}
		locked = append(locked, room)
	}
	return locked, nil
}
