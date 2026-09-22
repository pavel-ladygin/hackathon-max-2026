package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
	rooms "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

func openTestDB(t *testing.T) *store.Pool {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(db.Close)
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		t.Fatalf("check migrations: %v", err)
	}
	if got, err := platform.New(db.Pool).PlatformHealth(ctx); err != nil || got != 1 {
		t.Fatalf("platform sqlc health = %d, %v; want 1, nil", got, err)
	}
	if got, err := rooms.New(db.Pool).RoomsHealth(ctx); err != nil || got != 1 {
		t.Fatalf("rooms sqlc health = %d, %v; want 1, nil", got, err)
	}
	return db
}

func databaseNow(t *testing.T, db *store.Pool) time.Time {
	t.Helper()
	var now time.Time
	if err := db.QueryRow(context.Background(), "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatalf("read database clock: %v", err)
	}
	return now
}

func TestPostgresFoundationAndTransactions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	name := "foundation_smoke_" + strings.ReplaceAll(fmt.Sprint(time.Now().UnixNano()), "-", "_")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, suffix := range []string{"", "_error", "_panic", "_cancel"} {
			_, _ = db.Exec(cleanupCtx, "DROP TABLE IF EXISTS "+name+suffix)
		}
	})

	t.Run("commit", func(t *testing.T) {
		if err := db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "CREATE TABLE "+name+" (id integer NOT NULL)")
			return err
		}); err != nil {
			t.Fatalf("commit transaction: %v", err)
		}
		var exists bool
		if err := db.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists); err != nil || !exists {
			t.Fatalf("committed table exists = %v, err = %v", exists, err)
		}
	})

	t.Run("returned error rolls back", func(t *testing.T) {
		rollbackName := name + "_error"
		errSentinel := errors.New("rollback sentinel")
		err := db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "CREATE TABLE "+rollbackName+" (id integer NOT NULL)"); err != nil {
				return err
			}
			return errSentinel
		})
		if !errors.Is(err, errSentinel) {
			t.Fatalf("error = %v, want sentinel", err)
		}
		assertTableAbsent(t, db, rollbackName)
	})

	t.Run("panic rolls back", func(t *testing.T) {
		panicName := name + "_panic"
		func() {
			defer func() {
				if recover() == nil {
					t.Error("transaction panic was not propagated")
				}
			}()
			_ = db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, "CREATE TABLE "+panicName+" (id integer NOT NULL)"); err != nil {
					return err
				}
				panic("transaction panic")
			})
		}()
		assertTableAbsent(t, db, panicName)
	})

	t.Run("callback cancellation rolls back", func(t *testing.T) {
		cancelName := name + "_cancel"
		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		err := db.InTx(cancelCtx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(cancelCtx, "CREATE TABLE "+cancelName+" (id integer NOT NULL)"); err != nil {
				return err
			}
			cancel()
			return cancelCtx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
		assertTableAbsent(t, db, cancelName)
	})
}

func assertTableAbsent(t *testing.T, db *store.Pool, name string) {
	t.Helper()
	var exists bool
	if err := db.QueryRow(context.Background(), "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists); err != nil {
		t.Fatalf("check rollback table: %v", err)
	}
	if exists {
		t.Fatalf("table %s still exists after rollback", name)
	}
}
