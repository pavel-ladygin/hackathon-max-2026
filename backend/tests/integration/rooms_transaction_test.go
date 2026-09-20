package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/matching"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func TestRoomFoundationTransactionAndBehaviorAtomicity(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	svc := rooms.NewService(db)
	abort := errors.New("abort room transaction")
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			event := contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: f.creator, RoomID: &f.room, Type: "room_create", OccurredAt: time.Now()}
			err := svc.WithTx(ctx, func(repo *rooms.Repository) error {
				var isolation string
				if err := repo.DBTX().QueryRow(ctx, "SHOW transaction_isolation").Scan(&isolation); err != nil {
					return err
				}
				if isolation != "read committed" {
					return fmt.Errorf("unexpected isolation: %s", isolation)
				}
				tx, ok := repo.DBTX().(pgx.Tx)
				if !ok || matching.NewRepository(tx).DBTX() != repo.DBTX() {
					return errors.New("room and matching repositories do not share the transaction")
				}
				if _, err := repo.LockRooms(ctx, f.room); err != nil {
					return err
				}
				if _, err := repo.Queries.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: f.room, UserID: f.creator, Role: "creator"}); err != nil {
					return err
				}
				if err := (behavior.Recorder{}).Record(ctx, repo.DBTX(), event); err != nil {
					return err
				}
				if !commit {
					return abort
				}
				return nil
			})
			if commit && err != nil || !commit && !errors.Is(err, abort) {
				t.Fatalf("transaction result: %v", err)
			}
			want := int64(0)
			if commit {
				want = 1
			}
			if n, err := roomsql.New(db).CountRoomMembers(ctx, f.room); err != nil || n != want {
				t.Fatalf("member rows = %d, %v; want %d", n, err, want)
			}
			var n int64
			if err := db.QueryRow(ctx, "SELECT count(*) FROM behavior_events WHERE id=$1", event.ID).Scan(&n); err != nil || n != want {
				t.Fatalf("behavior rows = %d, %v; want %d", n, err, want)
			}
		})
	}
}

func TestRoomFoundationLockRoomsOrderingAndMissingRoom(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	other := f.insertRoom(t, f.member)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	svc := rooms.NewService(db)
	if err := svc.WithTx(ctx, func(repo *rooms.Repository) error {
		locked, err := repo.LockRooms(ctx, other, f.room, other)
		if err != nil {
			return err
		}
		if len(locked) != 2 || string(locked[0].ID[:]) >= string(locked[1].ID[:]) {
			return fmt.Errorf("expected two distinct rooms sorted by UUID, got %v", locked)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.WithTx(ctx, func(repo *rooms.Repository) error {
		_, err := repo.LockRooms(ctx, uuid.Nil)
		return err
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing room error = %v, want ErrNoRows", err)
	}
}

func TestRoomFoundationBulkPoolRollback(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	events := []uuid.UUID{f.newEvent(t), f.newEvent(t)}
	poolID := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := rooms.NewService(db).WithTx(ctx, func(repo *rooms.Repository) error {
		if _, err := repo.LockRooms(ctx, f.room); err != nil {
			return err
		}
		if _, err := repo.Queries.InsertRoomPool(ctx, roomsql.InsertRoomPoolParams{
			ID: poolID, RoomID: f.room, Version: 1, RoundNo: 1, RankerVersion: "test",
			InputFingerprint: poolID.String(), State: "ready", CandidateCount: 2,
			IsSmall: true, Diagnostics: []byte(`{}`),
		}); err != nil {
			return err
		}
		// Duplicate position fails halfway through a batch; metadata must roll back too.
		var batch []roomsql.InsertRoomPoolEventsParams
		for _, event := range events {
			batch = append(batch, roomsql.InsertRoomPoolEventsParams{
				PoolID: poolID, EventID: event, Position: 0,
				Explanation: []byte(`[]`), FeatureSnapshot: []byte(`{}`),
			})
		}
		_, err := repo.Queries.InsertRoomPoolEvents(ctx, batch)
		return err
	})
	assertPGCode(t, err, "23505")
	for _, query := range []string{
		"SELECT count(*) FROM room_pools WHERE id=$1",
		"SELECT count(*) FROM room_pool_events WHERE pool_id=$1",
	} {
		var count int
		if err := db.QueryRow(ctx, query, poolID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed batch left rows: count=%d, err=%v", count, err)
		}
	}
}

func TestRoomFoundationConcurrentMembershipInsert(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	svc := rooms.NewService(db)
	if err := svc.WithTx(ctx, func(repo *rooms.Repository) error {
		if _, err := repo.LockRooms(ctx, f.room); err != nil {
			return err
		}
		_, err := repo.Queries.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: f.room, UserID: f.creator, Role: "creator"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for _, user := range []uuid.UUID{f.member, f.third} {
		go func() {
			<-start
			results <- svc.WithTx(ctx, func(repo *rooms.Repository) error {
				if _, err := repo.Queries.LockMembershipUser(ctx, user); err != nil {
					return err
				}
				if _, err := repo.LockRooms(ctx, f.room); err != nil {
					return err
				}
				_, err := repo.Queries.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: f.room, UserID: user, Role: "participant"})
				return err
			})
		}()
	}
	close(start)
	var successes, full int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, pgx.ErrNoRows):
			full++
		default:
			t.Errorf("concurrent insert: %v", err)
		}
	}
	if successes != 1 || full != 1 {
		t.Fatalf("got %d inserted and %d full; want one each", successes, full)
	}
	if count, err := roomsql.New(db).CountRoomMembers(ctx, f.room); err != nil || count != 2 {
		t.Fatalf("membership count=%d, err=%v; want two", count, err)
	}
}
