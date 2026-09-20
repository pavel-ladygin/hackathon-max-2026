package integration

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

// seedB8VotingPool creates the smallest durable state accepted by the vote
// transaction: two active members, one ready pool, and its immutable event
// membership. It deliberately uses the same fixture helpers as the other room
// integration tests instead of going through an HTTP handler.
func seedB8VotingPool(t *testing.T, db *store.Pool, eventCount int) (*roomFixture, roomsql.RoomPool, []uuid.UUID) {
	t.Helper()
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	q := roomsql.New(db)
	ctx := context.Background()
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := q.InsertRoomRoundState(ctx, roomsql.InsertRoomRoundStateParams{RoomID: f.room, UserID: user, RoundNo: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := q.UpsertRoomIntent(ctx, intentParams(f, user, "vote fixture", 1000)); err != nil {
			t.Fatal(err)
		}
	}
	poolID := uuid.New()
	pool, err := q.InsertRoomPool(ctx, roomsql.InsertRoomPoolParams{
		ID: poolID, RoomID: f.room, Version: 1, RoundNo: 1, RankerVersion: "b8-test",
		InputFingerprint: poolID.String(), State: "ready", CandidateCount: int32(eventCount),
		IsSmall: eventCount > 0 && eventCount < 3, Diagnostics: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]uuid.UUID, eventCount)
	rows := make([]roomsql.InsertRoomPoolEventsParams, 0, eventCount)
	for i := range events {
		events[i] = f.newEvent(t)
		if _, err := db.Exec(ctx, "UPDATE events SET price_from_minor=100,currency='RUB',ticket_url='https://tickets.example/vote',ticket_available=true WHERE id=$1", events[i]); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, roomsql.InsertRoomPoolEventsParams{
			PoolID: poolID, EventID: events[i], Position: int32(i),
			Explanation: []byte(`[]`), FeatureSnapshot: []byte(`{}`),
		})
	}
	if _, err := q.InsertRoomPoolEvents(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='voting', active_pool_version=1 WHERE id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	return f, pool, events
}

func TestB8VoteInsertIsImmutableAndIdempotent(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	ctx := context.Background()
	q := roomsql.New(db)
	arg := roomsql.InsertRoomVoteParams{PoolID: pool.ID, RoomID: f.room, EventID: events[0], UserID: f.creator, Vote: "like"}
	if n, err := q.InsertRoomVote(ctx, arg); err != nil || n != 1 {
		t.Fatalf("first vote=%d err=%v; want one insert", n, err)
	}
	if n, err := q.InsertRoomVote(ctx, arg); err != nil || n != 0 {
		t.Fatalf("same retry=%d err=%v; want idempotent no-op", n, err)
	}
	arg.Vote = "dislike"
	if n, err := q.InsertRoomVote(ctx, arg); err != nil || n != 0 {
		t.Fatalf("mutation=%d err=%v; want conflict/no-op", n, err)
	}
	stored, err := q.GetRoomVote(ctx, roomsql.GetRoomVoteParams{PoolID: pool.ID, EventID: events[0], UserID: f.creator})
	if err != nil || stored.Vote != "like" {
		t.Fatalf("stored vote=%q err=%v; want immutable like", stored.Vote, err)
	}
	var behaviors int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type IN ('like','dislike')", f.room).Scan(&behaviors); err != nil {
		t.Fatal(err)
	}
	if behaviors != 0 {
		t.Fatalf("raw persistence inserted %d behavior rows; vote behavior must be transaction-owned", behaviors)
	}
}

func TestB8VoteMembershipAndPoolVersionGuards(t *testing.T) {
	db := openTestDB(t)
	f, pool, _ := seedB8VotingPool(t, db, 1)
	ctx := context.Background()
	q := roomsql.New(db)
	if _, err := q.GetRoomPoolEvent(ctx, roomsql.GetRoomPoolEventParams{PoolID: pool.ID, EventID: uuid.New()}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("event outside immutable pool err=%v; want pgx.ErrNoRows", err)
	}
	var activeVersion int32
	if err := db.QueryRow(ctx, "SELECT active_pool_version FROM rooms WHERE id=$1 AND active_pool_version=$2", f.room, 999).Scan(&activeVersion); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale pool query err=%v; want no matching active version", err)
	}
	outsider := f.third
	if _, err := q.GetRoomMembership(ctx, roomsql.GetRoomMembershipParams{RoomID: f.room, UserID: outsider}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign membership err=%v; want pgx.ErrNoRows", err)
	}
}

func newVoteService(t *testing.T, db *store.Pool, recorder contracts.BehaviorRecorder) *rooms.Service {
	t.Helper()
	svc := newCreateService(t, db, recorder)
	codec, err := rooms.NewRoomEventsCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.EnableRoomEvents(catalog.NewRepository(db), codec); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestB8ServiceVoteIdempotencyAndExhaustion(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	svc := newVoteService(t, db, behavior.Recorder{})
	request := api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Dislike}
	first, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.creator}, f.room, events[0], request)
	if err != nil || !first.MyPoolFinished || first.RoomExhausted {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if _, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.creator}, f.room, events[0], request); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	request.Vote = api.Like
	if _, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.creator}, f.room, events[0], request); !errors.Is(err, rooms.ErrVoteAlreadyCast) {
		t.Fatalf("mutation err=%v", err)
	}
	request.Vote = api.Dislike
	second, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.member}, f.room, events[0], request)
	if err != nil || !second.MyPoolFinished || !second.RoomExhausted {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	var behaviors int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type='dislike'", f.room).Scan(&behaviors); err != nil || behaviors != 2 {
		t.Fatalf("behaviors=%d err=%v", behaviors, err)
	}
}

func TestB8ServiceCreatesMatchAndRollsBackRecorderFailure(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	svc := newVoteService(t, db, behavior.Recorder{})
	request := api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Like}
	if response, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.creator}, f.room, events[0], request); err != nil {
		t.Fatalf("first like=%+v err=%v", response, err)
	} else if _, matchErr := response.Match.Get(); matchErr == nil {
		t.Fatalf("first like unexpectedly returned match: %+v", response)
	}
	response, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.member}, f.room, events[0], request)
	match, matchErr := response.Match.Get()
	if err != nil || matchErr != nil || match.Event.Id != events[0] {
		t.Fatalf("match=%+v matchErr=%v err=%v", match, matchErr, err)
	}

	f2, pool2, events2 := seedB8VotingPool(t, db, 1)
	failing := newVoteService(t, db, failingRecorder{err: errors.New("recorder unavailable")})
	_, err = failing.Vote(context.Background(), contracts.Principal{UserID: f2.creator}, f2.room, events2[0], api.VoteRequest{PoolVersion: int(pool2.Version), Vote: api.Like})
	if err == nil {
		t.Fatal("expected recorder failure")
	}
	var votes int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM room_votes WHERE room_id=$1", f2.room).Scan(&votes); err != nil || votes != 0 {
		t.Fatalf("votes=%d err=%v", votes, err)
	}
}

func TestB8VoteFinishAndExhaustedTransition(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 2)
	ctx := context.Background()
	q := roomsql.New(db)
	for _, event := range events {
		if n, err := q.InsertRoomVote(ctx, roomsql.InsertRoomVoteParams{PoolID: pool.ID, RoomID: f.room, EventID: event, UserID: f.creator, Vote: "dislike"}); err != nil || n != 1 {
			t.Fatalf("creator vote=%d err=%v", n, err)
		}
	}
	if _, err := q.MarkPoolFinished(ctx, roomsql.MarkPoolFinishedParams{RoomID: f.room, UserID: f.creator, RoundNo: 1}); err != nil {
		t.Fatal(err)
	}
	done, err := q.AreBothPoolFinished(ctx, roomsql.AreBothPoolFinishedParams{RoomID: f.room, RoundNo: 1})
	if err != nil || done.Bool {
		t.Fatalf("one finished=%+v err=%v; room must remain voting", done, err)
	}
	var state string
	if err := db.QueryRow(ctx, "SELECT state FROM rooms WHERE id=$1", f.room).Scan(&state); err != nil || state != "voting" {
		t.Fatalf("state after one finish=%q err=%v; want voting", state, err)
	}
	for _, event := range events {
		if n, err := q.InsertRoomVote(ctx, roomsql.InsertRoomVoteParams{PoolID: pool.ID, RoomID: f.room, EventID: event, UserID: f.member, Vote: "dislike"}); err != nil || n != 1 {
			t.Fatalf("member vote=%d err=%v", n, err)
		}
	}
	if _, err := q.MarkPoolFinished(ctx, roomsql.MarkPoolFinishedParams{RoomID: f.room, UserID: f.member, RoundNo: 1}); err != nil {
		t.Fatal(err)
	}
	done, err = q.AreBothPoolFinished(ctx, roomsql.AreBothPoolFinishedParams{RoomID: f.room, RoundNo: 1})
	if err != nil || !done.Bool {
		t.Fatalf("both finished=%+v err=%v; want true", done, err)
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='exhausted' WHERE id=$1 AND state='voting'", f.room); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT state FROM rooms WHERE id=$1", f.room).Scan(&state); err != nil || state != "exhausted" {
		t.Fatalf("final state=%q err=%v; want exhausted", state, err)
	}
}

func TestB8MatchIsAtomicAndUniqueUnderConcurrency(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 2)
	ctx := context.Background()
	q := roomsql.New(db)
	for _, event := range events {
		for _, user := range []uuid.UUID{f.creator, f.member} {
			if _, err := q.InsertRoomVote(ctx, roomsql.InsertRoomVoteParams{PoolID: pool.ID, RoomID: f.room, EventID: event, UserID: user, Vote: "like"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	start := make(chan struct{})
	results := make(chan int64, 2)
	var wg sync.WaitGroup
	for _, event := range events {
		wg.Add(1)
		go func(event uuid.UUID) {
			defer wg.Done()
			<-start
			tx, err := db.Begin(ctx)
			if err != nil {
				results <- -1
				return
			}
			n, err := roomsql.New(tx).InsertRoomMatch(ctx, roomsql.InsertRoomMatchParams{ID: uuid.New(), RoomID: f.room, PoolID: pool.ID, EventID: event})
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
			if err != nil {
				results <- -1
				return
			}
			results <- n
		}(event)
	}
	close(start)
	wg.Wait()
	close(results)
	var inserted int
	for n := range results {
		if n < 0 {
			t.Fatalf("concurrent match insert failed")
		}
		inserted += int(n)
	}
	if inserted != 1 {
		t.Fatalf("concurrent match inserts=%d; want exactly one", inserted)
	}
	var matches int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_matches WHERE room_id=$1", f.room).Scan(&matches); err != nil || matches != 1 {
		t.Fatalf("room matches=%d err=%v; want one", matches, err)
	}
}

func TestB8VoteTransactionRollsBackVoteAndMatchTogether(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	ctx := context.Background()
	errSentinel := errors.New("recorder unavailable")
	err := db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := roomsql.New(tx)
		if _, err := q.InsertRoomVote(ctx, roomsql.InsertRoomVoteParams{PoolID: pool.ID, RoomID: f.room, EventID: events[0], UserID: f.creator, Vote: "like"}); err != nil {
			return err
		}
		if _, err := q.InsertRoomMatch(ctx, roomsql.InsertRoomMatchParams{ID: uuid.New(), RoomID: f.room, PoolID: pool.ID, EventID: events[0]}); err != nil {
			return err
		}
		return errSentinel
	})
	if !errors.Is(err, errSentinel) {
		t.Fatalf("transaction err=%v; want sentinel", err)
	}
	var votes, matches int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_votes WHERE room_id=$1", f.room).Scan(&votes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_matches WHERE room_id=$1", f.room).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if votes != 0 || matches != 0 {
		t.Fatalf("rollback left votes=%d matches=%d; want both zero", votes, matches)
	}
}
