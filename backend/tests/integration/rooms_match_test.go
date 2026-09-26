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
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// TestB9ConcurrentMutualLikesCommitOneTerminalMatch exercises the service
// boundary (including its room lock), rather than inserting rows directly.
func TestB9ConcurrentMutualLikesCommitOneTerminalMatch(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	svc := newVoteService(t, db, behavior.Recorder{})
	request := api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Like}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		go func(user uuid.UUID) {
			<-start
			_, err := svc.Vote(context.Background(), contracts.Principal{UserID: user}, f.room, events[0], request)
			results <- err
		}(user)
	}
	close(start)
	var success int
	for range 2 {
		if err := <-results; err != nil {
			t.Errorf("concurrent vote: %v", err)
		} else {
			success++
		}
	}
	if success != 2 {
		t.Fatalf("successful votes=%d; want both callers to commit", success)
	}

	assertB9TerminalState(t, db, f.room, events[0], f.creator, f.member)
	var matches, terminalEvents int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM room_matches WHERE room_id=$1", f.room).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type='match_created'", f.room).Scan(&terminalEvents); err != nil {
		t.Fatal(err)
	}
	if matches != 1 || terminalEvents != 1 {
		t.Fatalf("matches=%d terminal behavior=%d; want one each", matches, terminalEvents)
	}
}

func TestB9CompetingEventsProduceOneMatchAndStableReload(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 2)
	svc := newVoteService(t, db, behavior.Recorder{})
	request := api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Like}
	// Establish one side's intent on both candidate events first.
	for _, event := range events {
		if _, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.creator}, f.room, event, request); err != nil {
			t.Fatalf("creator like %s: %v", event, err)
		}
	}

	start := make(chan struct{})
	results := make(chan error, len(events))
	var wg sync.WaitGroup
	for _, event := range events {
		wg.Add(1)
		go func(event uuid.UUID) {
			defer wg.Done()
			<-start
			_, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.member}, f.room, event, request)
			results <- err
		}(event)
	}
	close(start)
	wg.Wait()
	close(results)
	var succeeded, alreadyMatched int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, rooms.ErrAlreadyMatched):
			alreadyMatched++
		default:
			t.Errorf("competing vote: %v", err)
		}
	}
	if succeeded != 1 || alreadyMatched != 1 {
		t.Fatalf("competing results succeeded=%d already_matched=%d; want one each", succeeded, alreadyMatched)
	}

	ctx := context.Background()
	var matchedEvent uuid.UUID
	if err := db.QueryRow(ctx, "SELECT matched_event_id FROM rooms WHERE id=$1", f.room).Scan(&matchedEvent); err != nil {
		t.Fatal(err)
	}
	var matchCount int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_matches WHERE room_id=$1", f.room).Scan(&matchCount); err != nil {
		t.Fatal(err)
	}
	if matchCount != 1 || (matchedEvent != events[0] && matchedEvent != events[1]) {
		t.Fatalf("match count=%d matched event=%s; want one candidate", matchCount, matchedEvent)
	}

	first, err := svc.Get(ctx, contracts.Principal{UserID: f.creator}, f.room)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Get(ctx, contracts.Principal{UserID: f.member}, f.room)
	if err != nil {
		t.Fatal(err)
	}
	firstMatch, err := first.Match.Get()
	if err != nil {
		t.Fatal("creator reload has no match")
	}
	secondMatch, err := second.Match.Get()
	if err != nil {
		t.Fatal("member reload has no match")
	}
	if firstMatch.EventId != secondMatch.EventId || firstMatch.RoomId != secondMatch.RoomId || firstMatch.Id != secondMatch.Id || firstMatch.EventId != matchedEvent {
		t.Fatalf("reload summaries differ: creator=%+v member=%+v", firstMatch, secondMatch)
	}
	if len(first.AllowedActions) != 1 || first.AllowedActions[0] != api.ViewMatch || len(second.AllowedActions) != 1 || second.AllowedActions[0] != api.ViewMatch {
		t.Fatalf("terminal allowed actions: creator=%v member=%v; want view_match", first.AllowedActions, second.AllowedActions)
	}
	for _, snapshot := range []api.RoomSnapshot{first, second} {
		for _, participant := range snapshot.Participants {
			if participant.Id == uuid.Nil {
				t.Fatal("public participant has nil id")
			}
		}
	}
	newEvent := events[0]
	if newEvent == matchedEvent {
		newEvent = events[1]
	}
	if _, err := svc.Vote(ctx, contracts.Principal{UserID: f.member}, f.room, newEvent, request); !errors.Is(err, rooms.ErrAlreadyMatched) {
		t.Fatalf("post-match member vote=%v; want ErrAlreadyMatched", err)
	}
	if _, err := svc.Vote(ctx, contracts.Principal{UserID: f.third}, f.room, matchedEvent, request); !errors.Is(err, rooms.ErrRoomNotFound) {
		t.Fatalf("outsider vote=%v; want ErrRoomNotFound", err)
	}
}

func TestB9MatchTerminalRetiresMembershipsAndClearsCoordinates(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	svc := newVoteService(t, db, behavior.Recorder{})
	request := api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Like}
	if _, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.creator}, f.room, events[0], request); err != nil {
		t.Fatal(err)
	}
	response, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.member}, f.room, events[0], request)
	if err != nil {
		t.Fatal(err)
	}
	match, err := response.Match.Get()
	if err != nil || match.Event.Id != events[0] || len(match.Participants) != 2 {
		t.Fatalf("vote match=%+v err=%v; want event and both public participants", match, err)
	}
	retried, err := svc.Vote(context.Background(), contracts.Principal{UserID: f.member}, f.room, events[0], request)
	if err != nil {
		t.Fatalf("retry terminal vote: %v", err)
	}
	retriedMatch, err := retried.Match.Get()
	if err != nil || retriedMatch.Id != match.Id || retriedMatch.Event.Id != match.Event.Id {
		t.Fatalf("retried match=%+v err=%v; want stable match %s", retriedMatch, err, match.Id)
	}

	ctx := context.Background()
	var inactive, withCoordinates int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1 AND is_active=false", f.room).Scan(&inactive); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_intents WHERE room_id=$1 AND (location_lat IS NOT NULL OR location_lng IS NOT NULL)", f.room).Scan(&withCoordinates); err != nil {
		t.Fatal(err)
	}
	if inactive != 2 || withCoordinates != 0 {
		t.Fatalf("inactive memberships=%d intents with coordinates=%d; want 2 and 0", inactive, withCoordinates)
	}
}

func TestB9MatchRecorderFailureRollsBackTerminalTransition(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	first, err := newVoteService(t, db, behavior.Recorder{}).Vote(context.Background(), contracts.Principal{UserID: f.creator}, f.room, events[0], api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Like})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Match.Get(); err == nil {
		t.Fatal("first like unexpectedly matched")
	}

	sentinel := errors.New("match recorder unavailable")
	_, err = newVoteService(t, db, failOnMatchRecorder{err: sentinel}).Vote(context.Background(), contracts.Principal{UserID: f.member}, f.room, events[0], api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Like})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v; want recorder error", err)
	}
	var votes, matches, matchBehaviors int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM room_votes WHERE room_id=$1", f.room).Scan(&votes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM room_matches WHERE room_id=$1", f.room).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type='match_created'", f.room).Scan(&matchBehaviors); err != nil {
		t.Fatal(err)
	}
	if votes != 1 || matches != 0 || matchBehaviors != 0 {
		t.Fatalf("rollback state votes=%d matches=%d match behaviors=%d; want 1,0,0", votes, matches, matchBehaviors)
	}
}

// TestB9IdempotentVoteDoesNotDuplicatePersistence verifies the service-level
// retry contract: repeating the same vote succeeds without inserting another
// vote row or recording another behavior event.
func TestB9IdempotentVoteDoesNotDuplicatePersistence(t *testing.T) {
	db := openTestDB(t)
	f, pool, events := seedB8VotingPool(t, db, 1)
	svc := newVoteService(t, db, behavior.Recorder{})
	principal := contracts.Principal{UserID: f.creator}
	request := api.VoteRequest{PoolVersion: int(pool.Version), Vote: api.Dislike}

	first, err := svc.Vote(context.Background(), principal, f.room, events[0], request)
	if err != nil {
		t.Fatalf("first vote=%+v err=%v; want success", first, err)
	}
	if _, err := db.Exec(context.Background(), "UPDATE events SET ticket_available=false WHERE id=$1", events[0]); err != nil {
		t.Fatal(err)
	}
	second, err := svc.Vote(context.Background(), principal, f.room, events[0], request)
	if err != nil {
		t.Fatalf("idempotent retry after availability change=%+v err=%v; want success", second, err)
	}

	ctx := context.Background()
	var votes, behaviors int
	if err := db.QueryRow(ctx, `
		SELECT count(*) FROM room_votes
		WHERE room_id=$1 AND event_id=$2 AND user_id=$3`,
		f.room, events[0], f.creator).Scan(&votes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `
		SELECT count(*) FROM behavior_events
		WHERE room_id=$1 AND event_id=$2 AND user_id=$3 AND type='event_disliked'`,
		f.room, events[0], f.creator).Scan(&behaviors); err != nil {
		t.Fatal(err)
	}
	if votes != 1 || behaviors != 1 {
		t.Fatalf("retry persistence votes=%d behavior_events=%d; want one row each", votes, behaviors)
	}
}

func assertB9TerminalState(t *testing.T, db *store.Pool, roomID, eventID, creator, member uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var state string
	var matched uuid.UUID
	if err := db.QueryRow(ctx, "SELECT state, matched_event_id FROM rooms WHERE id=$1", roomID).Scan(&state, &matched); err != nil {
		t.Fatal(err)
	}
	if state != "matched" || matched != eventID {
		t.Fatalf("room state=%q matched event=%s; want matched/%s", state, matched, eventID)
	}
	for _, user := range []uuid.UUID{creator, member} {
		var active bool
		if err := db.QueryRow(ctx, "SELECT is_active FROM room_members WHERE room_id=$1 AND user_id=$2", roomID, user).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active {
			t.Fatalf("membership %s remains active", user)
		}
	}
}

type failOnMatchRecorder struct{ err error }

func (r failOnMatchRecorder) Record(ctx context.Context, db store.DBTX, event contracts.ServerBehaviorEvent) error {
	if event.Type == "match" {
		return r.err
	}
	return (behavior.Recorder{}).Record(ctx, db, event)
}
