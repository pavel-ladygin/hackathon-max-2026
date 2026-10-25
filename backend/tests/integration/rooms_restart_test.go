package integration

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
)

// exhaustRoundOne creates the normal B6 pool and marks it exhausted without
// mutating its intent history.  Restart behavior must preserve that history.
func exhaustRoundOne(t *testing.T, f *roomFixture, svc *rooms.Service) {
	t.Helper()
	if _, _, err := replaceIntentFromSnapshot(t, svc, f, f.creator, validIntentRequest()); err != nil {
		t.Fatal(err)
	}
	if _, transitioned, err := replaceIntentFromSnapshot(t, svc, f, f.member, validIntentRequest()); err != nil || !transitioned {
		t.Fatalf("seed pool transitioned=%v err=%v", transitioned, err)
	}
	if _, err := f.db.Exec(context.Background(), "UPDATE rooms SET state='exhausted' WHERE id=$1", f.room); err != nil {
		t.Fatal(err)
	}
}

func TestRestartExhaustedRoundCopiesDraftsAndRequiresBothConfirmations(t *testing.T) {
	f, svc := setupIntentRoom(t, behavior.Recorder{})
	exhaustRoundOne(t, f, svc)
	ctx := context.Background()

	first, transitioned, err := replaceIntentFromSnapshot(t, svc, f, f.creator, validIntentRequest())
	if err != nil || transitioned || first.State != api.RoomStateExhausted || first.RoundNo != 1 {
		t.Fatalf("restart first intent snapshot=%+v transitioned=%v err=%v", first, transitioned, err)
	}
	var round, activeVersion int
	var state string
	if err := f.db.QueryRow(ctx, "SELECT round_no, active_pool_version, state FROM rooms WHERE id=$1", f.room).Scan(&round, &activeVersion, &state); err != nil {
		t.Fatal(err)
	}
	if round != 1 || state != "exhausted" || activeVersion != 1 {
		t.Fatalf("room after first preparation round=%d state=%s active_version=%d; want 1/exhausted/1", round, state, activeVersion)
	}
	var drafts, ready int
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM room_intents WHERE room_id=$1 AND round_no=2", f.room).Scan(&drafts); err != nil || drafts != 2 {
		t.Fatalf("round 2 drafts=%d err=%v; want two copied intents", drafts, err)
	}
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND round_no=2 AND ready", f.room).Scan(&ready); err != nil || ready != 1 {
		t.Fatalf("round 2 ready=%d err=%v; want only restart initiator ready", ready, err)
	}
	var oldPool, newPool int
	if err := f.db.QueryRow(ctx, "SELECT count(*) FILTER (WHERE round_no=1), count(*) FILTER (WHERE round_no=2) FROM room_pools WHERE room_id=$1", f.room).Scan(&oldPool, &newPool); err != nil || oldPool != 1 || newPool != 0 {
		t.Fatalf("pool history old=%d new=%d err=%v; want 1/0", oldPool, newPool, err)
	}

	second, transitioned, err := replaceIntentFromSnapshot(t, svc, f, f.member, validIntentRequest())
	if err != nil || !transitioned || second.State != api.RoomStateVoting || second.RoundNo != 2 {
		t.Fatalf("restart second intent snapshot=%+v transitioned=%v err=%v", second, transitioned, err)
	}
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM room_pools WHERE room_id=$1 AND round_no=2", f.room).Scan(&newPool); err != nil || newPool != 1 {
		t.Fatalf("round 2 pools=%d err=%v; want one", newPool, err)
	}
}

func TestRestartExhaustedRoundConcurrentParticipantsCreatesOneRound(t *testing.T) {
	f, svc := setupIntentRoom(t, behavior.Recorder{})
	exhaustRoundOne(t, f, svc)
	source, err := svc.Get(context.Background(), contracts.Principal{UserID: f.creator}, f.room)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	var unconfirmed uuid.UUID
	for _, user := range []uuid.UUID{f.creator, f.member} {
		wg.Add(1)
		go func(user uuid.UUID) {
			defer wg.Done()
			<-start
			request := validIntentRequest()
			request.ExpectedRoundNo, request.ExpectedRoomVersion = source.RoundNo, source.Version
			_, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: user}, f.room, request, "intent-restart-"+user.String())
			errs <- err
		}(user)
	}
	close(start)
	wg.Wait()
	close(errs)
	confirmed, conflicted := 0, 0
	for err := range errs {
		if err == nil {
			confirmed++
		} else if errors.Is(err, rooms.ErrIntentConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if confirmed != 1 || conflicted != 1 {
		t.Fatalf("concurrent preparations confirmed=%d conflicts=%d; want 1/1", confirmed, conflicted)
	}
	for _, user := range []uuid.UUID{f.creator, f.member} {
		pending, err := svc.Get(context.Background(), contracts.Principal{UserID: user}, f.room)
		if err != nil {
			t.Fatal(err)
		}
		next, err := pending.NextRound.Get()
		if err != nil {
			t.Fatal(err)
		}
		if !next.MyConfirmed {
			unconfirmed = user
		}
	}
	if unconfirmed == uuid.Nil {
		t.Fatal("missing unconfirmed peer after one pending preparation")
	}
	if _, transitioned, err := replaceIntentFromSnapshot(t, svc, f, unconfirmed, validIntentRequest()); err != nil || !transitioned {
		t.Fatalf("fresh peer confirmation transitioned=%v err=%v", transitioned, err)
	}
	var round, states, intents, pools int
	var state string
	if err := f.db.QueryRow(context.Background(), "SELECT round_no, state FROM rooms WHERE id=$1", f.room).Scan(&round, &state); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND round_no=2", f.room).Scan(&states); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM room_intents WHERE room_id=$1 AND round_no=2", f.room).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM room_pools WHERE room_id=$1 AND round_no=2", f.room).Scan(&pools); err != nil {
		t.Fatal(err)
	}
	if round != 2 || state != "voting" || states != 2 || intents != 2 || pools != 1 {
		t.Fatalf("concurrent restart room=%d/%s states=%d intents=%d pools=%d; want 2/voting/2/2/1", round, state, states, intents, pools)
	}
}

func TestRestartRoundLimitDoesNotMutateTerminalRoom(t *testing.T) {
	f, svc := setupIntentRoom(t, behavior.Recorder{})
	ctx := context.Background()
	if _, err := f.db.Exec(ctx, "UPDATE rooms SET state='exhausted', round_no=3 WHERE id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	// Final-round exhaustion retires both memberships.  A former member must
	// still receive the stable round-limit error instead of an access-shaped
	// NOT_FOUND response when they try to restart the terminal room.
	if _, err := f.db.Exec(ctx, "UPDATE room_members SET is_active=false WHERE room_id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	request := validIntentRequest()
	request.ExpectedRoundNo, request.ExpectedRoomVersion = 3, 1
	_, _, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: f.creator}, f.room, request, "intent-round-limit")
	if !errors.Is(err, rooms.ErrRoundLimitReached) {
		t.Fatalf("restart at round three error=%v; want %v", err, rooms.ErrRoundLimitReached)
	}
	var round int
	var state string
	if err := f.db.QueryRow(ctx, "SELECT round_no, state FROM rooms WHERE id=$1", f.room).Scan(&round, &state); err != nil || round != 3 || state != "exhausted" {
		t.Fatalf("terminal room after rejected restart=%d/%s err=%v; want 3/exhausted", round, state, err)
	}
}
