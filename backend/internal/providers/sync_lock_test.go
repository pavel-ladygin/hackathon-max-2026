package providers

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

func TestProviderSyncLockRejectsConcurrentOwnerAndReleases(t *testing.T) {
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
	provider := "timepad-test-" + uuid.NewString()
	first, acquired, err := TryAcquireProviderSyncLock(context.Background(), db, provider)
	if err != nil || !acquired {
		t.Fatalf("first lock: acquired=%v err=%v", acquired, err)
	}
	second, acquired, err := TryAcquireProviderSyncLock(context.Background(), db, provider)
	if err != nil || acquired || second != nil {
		t.Fatalf("second lock: lock=%v acquired=%v err=%v", second, acquired, err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
	third, acquired, err := TryAcquireProviderSyncLock(context.Background(), db, provider)
	if err != nil || !acquired {
		t.Fatalf("lock after release: acquired=%v err=%v", acquired, err)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
}
