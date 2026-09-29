package providers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

const providerSyncLockReleaseTimeout = 5 * time.Second

// ProviderSyncLock is a session-scoped PostgreSQL advisory lock. Its dedicated
// connection makes the lock visible to every process using the same database.
type ProviderSyncLock struct {
	conn     *pgx.Conn
	lockKey  string
	mu       sync.Mutex
	released bool
}

// TryAcquireProviderSyncLock takes a non-blocking lock for one built-in
// provider. A busy lock is reported as (nil, false, nil).
func TryAcquireProviderSyncLock(ctx context.Context, db *store.Pool, provider string) (*ProviderSyncLock, bool, error) {
	if db == nil || db.Pool == nil {
		return nil, false, errors.New("provider sync database is required")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, false, errors.New("provider sync name is required")
	}
	lockKey := "provider-sync:" + provider
	conn, err := pgx.ConnectConfig(ctx, db.Config().ConnConfig.Copy())
	if err != nil {
		return nil, false, errors.New("provider sync lock unavailable")
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, lockKey).Scan(&acquired); err != nil {
		_ = conn.Close(ctx)
		return nil, false, errors.New("provider sync lock unavailable")
	}
	if !acquired {
		_ = conn.Close(ctx)
		return nil, false, nil
	}
	return &ProviderSyncLock{conn: conn, lockKey: lockKey}, true, nil
}

// Release is idempotent and uses a fresh bounded context so cancellation of
// the import cannot strand a session lock.
func (lock *ProviderSyncLock) Release() error {
	if lock == nil {
		return nil
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.released {
		return nil
	}
	lock.released = true
	ctx, cancel := context.WithTimeout(context.Background(), providerSyncLockReleaseTimeout)
	defer cancel()
	var released bool
	err := lock.conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lock.lockKey).Scan(&released)
	closeErr := lock.conn.Close(ctx)
	if err != nil || !released {
		return errors.New("provider sync lock release failed")
	}
	return closeErr
}
