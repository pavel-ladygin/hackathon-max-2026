package eventsources

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

func TestSourceSyncLockRejectsSecondOwnerAndCanBeReleased(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	db := &store.Pool{Pool: pool}
	defer db.Close()
	sourceKey := "generic:lock-test:" + uuid.NewString()
	first, err := acquireSourceSyncLock(context.Background(), db, sourceKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireSourceSyncLock(context.Background(), db, sourceKey); err != ErrSyncRunning {
		t.Fatalf("second acquisition error=%v, want ErrSyncRunning", err)
	}
	// The source lock uses a dedicated connection; it must not consume the sole
	// pooled connection needed by importRecords.
	pooled, err := db.Acquire(context.Background())
	if err != nil {
		t.Fatalf("pool acquire while source lock is held: %v", err)
	}
	pooled.Release()
	first.Release()
	third, err := acquireSourceSyncLock(context.Background(), db, sourceKey)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	third.Release()
}
