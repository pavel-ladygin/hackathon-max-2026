package integration

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func TestB10ConcurrentFinishDoesNotRelaxIntents(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	svc := newVoteService(t, db, behavior.Recorder{})
	ctx := context.Background()

	readFilters := func() []byte {
		t.Helper()
		var filters []byte
		err := db.QueryRow(ctx, `
			SELECT jsonb_agg(jsonb_build_object(
				'user_id', user_id,
				'date_options', date_options,
				'day_types', day_types,
				'time_slots', time_slots,
				'category_slugs', category_slugs,
				'exclusion_slugs', exclusion_slugs,
				'budget_max_minor', budget_max_minor,
				'radius_m', radius_m
			) ORDER BY user_id)::text::bytea
			FROM room_intents
			WHERE room_id=$1 AND round_no=1`, f.room).Scan(&filters)
		if err != nil {
			t.Fatal(err)
		}
		return filters
	}
	before := readFilters()

	type result struct {
		response api.VoteResponse
		err      error
	}
	results := make(chan result, 2)
	request := api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Dislike}
	for _, userID := range []uuid.UUID{f.creator, f.member} {
		go func(userID uuid.UUID) {
			response, err := svc.Vote(ctx, contracts.Principal{UserID: userID}, f.room, events[0], request)
			results <- result{response: response, err: err}
		}(userID)
	}

	exhaustedResponses := 0
	for range 2 {
		result := <-results
		if result.err != nil || !result.response.MyPoolFinished {
			t.Fatalf("concurrent finish response=%+v err=%v", result.response, result.err)
		}
		if result.response.RoomExhausted {
			exhaustedResponses++
		}
	}
	if exhaustedResponses != 1 {
		t.Fatalf("room_exhausted responses=%d; want exactly one transition response", exhaustedResponses)
	}

	var state string
	var finished int
	if err := db.QueryRow(ctx, "SELECT state FROM rooms WHERE id=$1", f.room).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND round_no=1 AND pool_finished", f.room).Scan(&finished); err != nil {
		t.Fatal(err)
	}
	if state != "exhausted" || finished != 2 {
		t.Fatalf("terminal state=%q finished=%d; want exhausted/2", state, finished)
	}
	if after := readFilters(); !reflect.DeepEqual(after, before) {
		t.Fatalf("intent filters changed during exhaustion: before=%s after=%s", before, after)
	}
}

func TestB10OneThenBothFinishAndReconnect(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	svc := newVoteService(t, db, behavior.Recorder{})
	ctx := context.Background()
	req := api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Dislike}

	first, err := svc.Vote(ctx, contracts.Principal{UserID: f.creator}, f.room, events[0], req)
	if err != nil || !first.MyPoolFinished || first.RoomExhausted {
		t.Fatalf("first finish=%+v err=%v; want only caller finished", first, err)
	}
	var state string
	if err := db.QueryRow(ctx, "SELECT state FROM rooms WHERE id=$1", f.room).Scan(&state); err != nil || state != "voting" {
		t.Fatalf("state after first finish=%q err=%v; want voting", state, err)
	}

	second, err := svc.Vote(ctx, contracts.Principal{UserID: f.member}, f.room, events[0], req)
	if err != nil || !second.MyPoolFinished || !second.RoomExhausted {
		t.Fatalf("second finish=%+v err=%v; want exhausted", second, err)
	}
	for _, user := range []uuid.UUID{f.creator, f.member} {
		snapshot, err := svc.Get(ctx, contracts.Principal{UserID: user}, f.room)
		if err != nil {
			t.Fatalf("reconnect user=%s: %v", user, err)
		}
		poolSummary, err := snapshot.Pool.Get()
		if err != nil || !poolSummary.RoomExhausted {
			t.Fatalf("reconnect user=%s pool=%+v err=%v; want room exhausted", user, poolSummary, err)
		}
		if len(snapshot.AllowedActions) != 1 || snapshot.AllowedActions[0] != api.RestartWithNewIntent {
			t.Fatalf("reconnect actions=%v; want restart_with_new_intent", snapshot.AllowedActions)
		}
	}
}

func TestB10UnavailableEventsPersistPoolFinished(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	svc := newVoteService(t, db, behavior.Recorder{})
	ctx := context.Background()
	if _, err := db.Exec(ctx, "UPDATE events SET ticket_available=false, ticket_url=NULL WHERE id=$1", events[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetEvents(ctx, contracts.Principal{UserID: f.creator}, f.room, rooms.RoomEventsInput{}); !errors.Is(err, rooms.ErrPoolExhausted) {
		t.Fatalf("events error=%v; want pool exhausted", err)
	}
	var finished bool
	if err := db.QueryRow(ctx, "SELECT pool_finished FROM room_member_round_state WHERE room_id=$1 AND user_id=$2 AND round_no=1", f.room, f.creator).Scan(&finished); err != nil || !finished {
		t.Fatalf("persisted pool_finished=%v err=%v; want true", finished, err)
	}
	var poolState string
	if err := db.QueryRow(ctx, "SELECT state FROM rooms WHERE id=$1", f.room).Scan(&poolState); err != nil || poolState != "voting" {
		t.Fatalf("room state=%q err=%v; want voting after one unavailable pool", poolState, err)
	}
	// The immutable pool membership remains present even though its event is unavailable.
	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_pool_events WHERE pool_id=$1", pool.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pool event count=%d err=%v; want immutable event retained", count, err)
	}
}

func TestB10Round1ZeroCandidatePoolFinishesBothAndPreservesRestartData(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	ctx := context.Background()
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := roomsql.New(db).InsertRoomRoundState(ctx, roomsql.InsertRoomRoundStateParams{RoomID: f.room, UserID: user, RoundNo: 1}); err != nil {
			t.Fatal(err)
		}
	}
	builder := &poolBuilderFake{result: contracts.BuildResult{
		RankerVersion: "b10-round1-zero", InputFingerprint: "b10-round1-zero",
		Diagnostics: contracts.ExhaustionDiagnostics{Reasons: []contracts.ExhaustionReason{{
			Code: "catalog_shortage", Text: "Недостаточно подходящих событий в каталоге.",
		}}},
	}}
	svc := poolService(t, db, builder)
	snapshot, transitioned, err := submitBothIntentsSnapshots(t, svc, f)
	if err != nil || !transitioned {
		t.Fatalf("round1 zero-candidate build transitioned=%v err=%v", transitioned, err)
	}
	pool, err := snapshot.Pool.Get()
	if err != nil || !pool.RoomExhausted || !pool.MyPoolFinished {
		t.Fatalf("round1 zero-candidate snapshot pool=%+v err=%v; want exhausted and finished", pool, err)
	}
	var finished int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND round_no=1 AND pool_finished", f.room).Scan(&finished); err != nil {
		t.Fatal(err)
	}
	if finished != 2 {
		t.Fatalf("round1 finished states=%d; want 2", finished)
	}
	var active, coordinates int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1 AND is_active", f.room).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_intents WHERE room_id=$1 AND (location_lat IS NOT NULL OR location_lng IS NOT NULL)", f.room).Scan(&coordinates); err != nil {
		t.Fatal(err)
	}
	if active != 2 || coordinates != 2 {
		t.Fatalf("round1 restart data active_members=%d coordinates=%d; want 2/2", active, coordinates)
	}
}

func TestB10CursorAtEndDoesNotFinishPool(t *testing.T) {
	db := openTestDB(t)
	f, _, events := seedB8VotingPool(t, db, 2)
	svc := newVoteService(t, db, behavior.Recorder{})
	ctx := context.Background()
	page, err := svc.GetEvents(ctx, contracts.Principal{UserID: f.creator}, f.room, rooms.RoomEventsInput{Limit: 1})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("first page items=%d err=%v; want one item", len(page.Items), err)
	}
	cursor := page.Items[0].Cursor
	if cursor == "" {
		t.Fatal("first page did not return item cursor")
	}
	second, err := svc.GetEvents(ctx, contracts.Principal{UserID: f.creator}, f.room, rooms.RoomEventsInput{Limit: 1, Cursor: cursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].Event.Id != events[1] {
		t.Fatalf("second page=%+v err=%v; want final event", second, err)
	}
	endCursor := second.Items[0].Cursor
	empty, err := svc.GetEvents(ctx, contracts.Principal{UserID: f.creator}, f.room, rooms.RoomEventsInput{Limit: 1, Cursor: endCursor})
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("end cursor response=%+v error=%v; want empty page without exhaustion", empty, err)
	}
	var finished bool
	if err := db.QueryRow(ctx, "SELECT pool_finished FROM room_member_round_state WHERE room_id=$1 AND user_id=$2 AND round_no=1", f.room, f.creator).Scan(&finished); err != nil || finished {
		t.Fatalf("cursor-at-end pool_finished=%v err=%v; cursor alone must not finish", finished, err)
	}
}

func prepareB10Round3(t *testing.T, f *roomFixture, pool roomsql.RoomPool) {
	t.Helper()
	ctx := context.Background()
	q := roomsql.New(f.db)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := q.InsertRoomRoundState(ctx, roomsql.InsertRoomRoundStateParams{RoomID: f.room, UserID: user, RoundNo: 3}); err != nil {
			t.Fatal(err)
		}
		intent := intentParams(f, user, "round3", 1000)
		intent.RoundNo = 3
		if _, err := q.UpsertRoomIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(ctx, "UPDATE room_pools SET round_no=3 WHERE id=$1", pool.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, "UPDATE rooms SET round_no=3 WHERE id=$1", f.room); err != nil {
		t.Fatal(err)
	}
}

func assertB10TerminalCleanup(t *testing.T, f *roomFixture) {
	t.Helper()
	ctx := context.Background()
	var active, coordinates int
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1 AND is_active", f.room).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM room_intents WHERE room_id=$1 AND (location_lat IS NOT NULL OR location_lng IS NOT NULL)", f.room).Scan(&coordinates); err != nil {
		t.Fatal(err)
	}
	if active != 0 || coordinates != 0 {
		t.Fatalf("round3 terminal cleanup active_members=%d coordinates=%d; want 0/0", active, coordinates)
	}
}

func TestB10Round3VotingExhaustionRetiresMembersAndCoordinates(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	prepareB10Round3(t, f, pool)
	svc := newVoteService(t, db, behavior.Recorder{})
	ctx := context.Background()
	req := api.VoteRequest{PoolVersion: 1, Vote: api.Dislike}
	if _, err := svc.Vote(ctx, contracts.Principal{UserID: f.creator}, f.room, events[0], req); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Vote(ctx, contracts.Principal{UserID: f.member}, f.room, events[0], req); err != nil {
		t.Fatal(err)
	}
	assertB10TerminalCleanup(t, f)
	var poolState string
	if err := db.QueryRow(ctx, "SELECT state FROM room_pools WHERE id=$1", pool.ID).Scan(&poolState); err != nil || poolState != "exhausted" {
		t.Fatalf("active pool state=%q err=%v; want exhausted with terminal room", poolState, err)
	}
	// Terminal cleanup retires memberships, but historical participants may
	// retry the terminal request; users who never joined cannot discover it.
	retried, err := svc.Vote(ctx, contracts.Principal{UserID: f.creator}, f.room, events[0], req)
	if err != nil || !retried.MyPoolFinished || !retried.RoomExhausted || retried.AcceptedVote != req.Vote {
		t.Fatalf("historical vote retry response=%+v error=%v; want accepted terminal result", retried, err)
	}
	if _, err := svc.GetEvents(ctx, contracts.Principal{UserID: f.creator}, f.room, rooms.RoomEventsInput{}); !errors.Is(err, rooms.ErrPoolExhausted) {
		t.Fatalf("historical events retry error=%v; want pool exhausted", err)
	}
	if _, err := svc.Vote(ctx, contracts.Principal{UserID: f.third}, f.room, events[0], req); !errors.Is(err, rooms.ErrRoomNotFound) {
		t.Fatalf("stranger terminal retry error=%v; want room not found", err)
	}
}

func TestB10Round3ZeroCandidatePoolTerminalCleanup(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	ctx := context.Background()
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := roomsql.New(db).InsertRoomRoundState(ctx, roomsql.InsertRoomRoundStateParams{RoomID: f.room, UserID: user, RoundNo: 3}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='collecting_intents', round_no=3 WHERE id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	builder := &poolBuilderFake{result: contracts.BuildResult{
		RankerVersion:    "b10-zero",
		InputFingerprint: "b10-zero",
		Diagnostics: contracts.ExhaustionDiagnostics{Reasons: []contracts.ExhaustionReason{{
			Code: "catalog_shortage", Text: "Недостаточно подходящих событий в каталоге.",
		}}},
	}}
	svc := poolService(t, db, builder)
	snapshot, transitioned, err := submitBothIntentsSnapshots(t, svc, f)
	if err != nil || !transitioned {
		t.Fatalf("round3 zero-candidate build transitioned=%v err=%v", transitioned, err)
	}
	poolSummary, err := snapshot.Pool.Get()
	if err != nil || !poolSummary.RoomExhausted || !poolSummary.MyPoolFinished {
		t.Fatalf("round3 zero-candidate snapshot pool=%+v err=%v; want exhausted and finished", poolSummary, err)
	}
	var finished int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND round_no=3 AND pool_finished", f.room).Scan(&finished); err != nil {
		t.Fatal(err)
	}
	if finished != 2 {
		t.Fatalf("round3 finished states=%d; want 2", finished)
	}
	var state string
	if err := db.QueryRow(ctx, "SELECT state FROM rooms WHERE id=$1", f.room).Scan(&state); err != nil || state != "exhausted" {
		t.Fatalf("zero-candidate state=%q err=%v; want exhausted", state, err)
	}
	assertB10TerminalCleanup(t, f)
}

func TestB10ExhaustionRetryIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	svc := newVoteService(t, db, behavior.Recorder{})
	ctx := context.Background()
	req := api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Dislike}
	if _, err := svc.Vote(ctx, contracts.Principal{UserID: f.creator}, f.room, events[0], req); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Vote(ctx, contracts.Principal{UserID: f.member}, f.room, events[0], req); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, contracts.Principal{UserID: f.creator}, f.room); err != nil {
		t.Fatal(err)
	}
	repeated, err := svc.Vote(ctx, contracts.Principal{UserID: f.creator}, f.room, events[0], req)
	if err != nil {
		t.Fatalf("repeat accepted vote: %v", err)
	}
	if repeated.AcceptedVote != api.Dislike || repeated.PoolVersion != int(pool.Version) || !repeated.MyPoolFinished || !repeated.RoomExhausted {
		t.Fatalf("repeat accepted vote response=%+v; want stable terminal result", repeated)
	}
	var finished, terminal int
	if err := db.QueryRow(ctx, "SELECT count(*) FILTER (WHERE pool_finished), count(*) FROM room_member_round_state s JOIN rooms r ON r.id=s.room_id WHERE s.room_id=$1 AND s.round_no=1", f.room).Scan(&finished, &terminal); err != nil {
		t.Fatal(err)
	}
	if finished != 2 || terminal != 2 {
		t.Fatalf("retry persistence finished=%d states=%d; want 2/2", finished, terminal)
	}
	var eventsCount int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type='event_disliked'", f.room).Scan(&eventsCount); err != nil || eventsCount != 2 {
		t.Fatalf("behavior events=%d err=%v; want exactly two votes", eventsCount, err)
	}
}
