package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

func TestCreateRoomBothMembersReplaceSameExhaustedRoom(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='exhausted' WHERE id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	svc := newCreateService(t, db, behavior.Recorder{})
	blocker, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	defer func() {
		cancel()
		_ = blocker.Rollback(context.Background())
		workers.Wait()
	}()
	var blockerPID int32
	if err := blocker.QueryRow(ctx, "SELECT pg_backend_pid() FROM rooms WHERE id=$1 FOR UPDATE", f.room).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	results := make(chan createResult, 2)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			response, err := svc.Create(ctx, contracts.Principal{UserID: user}, "shared-room-replace", api.CreateRoomRequest{CityId: f.city, Name: "replacement"})
			results <- createResult{response: response, err: err}
		}()
	}
	// Wait for actual PostgreSQL lock contention, not a guessed scheduling delay.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if err := db.QueryRow(ctx, `WITH RECURSIVE blocked(pid) AS (
			SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))
			UNION
			SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))
		) SELECT count(*) FROM blocked`, blockerPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("both create transactions did not reach the room lock")
		case <-ticker.C:
		}
	}
	var releasedAt time.Time
	if err := blocker.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&releasedAt); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	ids := make(map[uuid.UUID]bool)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent replacement: %v", result.err)
		}
		if result.response.Room.ExpiresAt.Before(releasedAt.Add(48 * time.Hour)) {
			t.Error("new room TTL was measured before the blocked room lock was acquired")
		}
		ids[result.response.Room.Id] = true
	}
	if len(ids) != 2 {
		t.Fatal("the two users must create separate new rooms")
	}
	var oldActive, currentActive int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1 AND is_active", f.room).Scan(&oldActive); err != nil || oldActive != 0 {
		t.Fatalf("old active=%d err=%v", oldActive, err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE user_id=ANY($1::uuid[]) AND is_active", []uuid.UUID{f.creator, f.member}).Scan(&currentActive); err != nil || currentActive != 2 {
		t.Fatalf("new active=%d err=%v", currentActive, err)
	}
}
