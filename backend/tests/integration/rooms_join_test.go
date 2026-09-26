package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

func TestJoinRoomPersistsSafeParticipantSnapshotAndIsRepeatable(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	svc := newCreateService(t, db, behavior.Recorder{})
	created := createJoinTarget(t, svc, f.creator, f.city, "join-success")

	joined, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "join-first-key")
	if err != nil {
		t.Fatal(err)
	}
	if joined.Id != created.Room.Id || joined.Version != 2 || len(joined.Participants) != 2 {
		t.Fatalf("unexpected joined snapshot: %+v", joined)
	}
	if !nullableIsNull(joined.Invite) || !nullableIsNull(joined.MyIntent) || !nullableIsNull(joined.Pool) || !nullableIsNull(joined.Match) {
		t.Fatalf("participant snapshot exposes creator/private state: %+v", joined)
	}
	if len(joined.AllowedActions) != 1 || joined.AllowedActions[0] != api.EditIntent {
		t.Fatalf("participant actions = %v; want edit_intent", joined.AllowedActions)
	}
	raw, err := json.Marshal(joined)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"token", "max_deep_link", "location_lat", "location_lng", "max_user_id"} {
		if strings.Contains(string(raw), private) {
			t.Errorf("join response leaks %q: %s", private, raw)
		}
	}

	retried, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "join-second-key")
	if err != nil {
		t.Fatalf("same participant retry with another key: %v", err)
	}
	assertIdenticalJSON(t, joined, retried)
	assertJoinRows(t, db, created.Room.Id, f.member, 1, 1)

	creatorView, err := svc.Join(ctx, contracts.Principal{UserID: f.creator}, created.Invite.Token, "creator-own-invite")
	if err != nil {
		t.Fatalf("creator own invite: %v", err)
	}
	if creatorView.Id != created.Room.Id || nullableIsNull(creatorView.Invite) {
		t.Fatalf("unexpected creator join response: %+v", creatorView)
	}
	if len(creatorView.AllowedActions) != 1 || creatorView.AllowedActions[0] != api.EditIntent {
		t.Fatalf("creator actions = %v; want edit_intent for a full room", creatorView.AllowedActions)
	}
	var creatorJoinEvents int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND user_id=$2 AND type='room_joined'", created.Room.Id, f.creator).Scan(&creatorJoinEvents); err != nil || creatorJoinEvents != 0 {
		t.Fatalf("creator own invite behavior count=%d err=%v; want zero", creatorJoinEvents, err)
	}

	if _, err := db.Exec(ctx, "UPDATE rooms SET state='voting', version=version+1 WHERE id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	votingRetry, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "join-voting-retry")
	if err != nil {
		t.Fatalf("same participant retry after voting transition: %v", err)
	}
	if votingRetry.State != api.RoomStateVoting || len(votingRetry.AllowedActions) != 2 || votingRetry.AllowedActions[0] != api.ViewPool || votingRetry.AllowedActions[1] != api.Vote {
		t.Fatalf("voting retry returned stale join snapshot: %+v", votingRetry)
	}

	if _, err := db.Exec(ctx, "UPDATE rooms SET state='exhausted', round_no=3, version=version+1 WHERE id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE room_members SET is_active=false WHERE room_id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	terminalRetry, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "join-terminal-retry")
	if err != nil {
		t.Fatalf("historical participant retry after retirement: %v", err)
	}
	if terminalRetry.State != api.RoomStateExhausted || len(terminalRetry.AllowedActions) != 0 {
		t.Fatalf("terminal retry returned stale join snapshot: %+v", terminalRetry)
	}
	assertJoinRows(t, db, created.Room.Id, f.member, 1, 1)
}

func TestJoinRoomRejectsInvalidUnknownExpiredFullAndActiveRoom(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	svc := newCreateService(t, db, behavior.Recorder{})
	created := createJoinTarget(t, svc, f.creator, f.city, "join-errors")

	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, "short", "valid-join-key"); !errors.Is(err, rooms.ErrValidation) {
		t.Fatalf("invalid token error=%v; want validation", err)
	}
	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, strings.Repeat("z", 43), "unknown-key"); !errors.Is(err, rooms.ErrInviteNotFound) {
		t.Fatalf("unknown token error=%v; want invite not found", err)
	}

	if _, err := db.Exec(ctx, "UPDATE room_invites SET expires_at=now()-interval '1 second' WHERE room_id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "expired-invite"); !errors.Is(err, rooms.ErrInviteExpired) {
		t.Fatalf("expired invite error=%v; want invite expired", err)
	}
	if _, err := db.Exec(ctx, "UPDATE room_invites SET expires_at=now()+interval '1 hour', consumed_by=NULL, consumed_at=NULL WHERE room_id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET expires_at=now()-interval '1 second' WHERE id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "expired-room"); !errors.Is(err, rooms.ErrInviteExpired) {
		t.Fatalf("expired room error=%v; want invite expired", err)
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET expires_at=now()+interval '1 hour' WHERE id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "fill-room"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.third}, created.Invite.Token, "third-user"); !errors.Is(err, rooms.ErrRoomFull) {
		t.Fatalf("full room error=%v; want room full", err)
	}

	targetCreator, activeUser := f.newUser(t), f.newUser(t)
	target := createJoinTarget(t, svc, targetCreator, f.city, "active-target")
	_ = createJoinTarget(t, svc, activeUser, f.city, "active-other")
	if _, err := svc.Join(ctx, contracts.Principal{UserID: activeUser}, target.Invite.Token, "active-conflict"); !errors.Is(err, rooms.ErrActiveRoomExists) {
		t.Fatalf("active other room error=%v; want active room exists", err)
	}
}

func TestJoinRoomRetiresRestartableRoomAndRollsBackRecorderFailure(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	svc := newCreateService(t, db, behavior.Recorder{})
	old := createJoinTarget(t, svc, f.member, f.city, "old-exhausted")
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='exhausted', round_no=2 WHERE id=$1", old.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO room_intents(room_id,user_id,round_no,date_options,day_types,time_slots,category_slugs,budget_max_minor,location_lat,location_lng,radius_m,exclusion_slugs) VALUES($1,$2,2,'{}','{}','{}','{}',0,55,37,100,'{}')", old.Room.Id, f.member); err != nil {
		t.Fatal(err)
	}
	target := createJoinTarget(t, svc, f.creator, f.city, "replacement-target")
	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, target.Invite.Token, "replace-join"); err != nil {
		t.Fatal(err)
	}
	var active bool
	var lat *float64
	var roomExpired, inviteExpired bool
	if err := db.QueryRow(ctx, "SELECT is_active FROM room_members WHERE room_id=$1 AND user_id=$2", old.Room.Id, f.member).Scan(&active); err != nil || active {
		t.Fatalf("old membership active=%v err=%v", active, err)
	}
	if err := db.QueryRow(ctx, "SELECT location_lat FROM room_intents WHERE room_id=$1 AND user_id=$2 AND round_no=2", old.Room.Id, f.member).Scan(&lat); err != nil || lat != nil {
		t.Fatalf("old coordinates=%v err=%v", lat, err)
	}
	if err := db.QueryRow(ctx, "SELECT expires_at <= now() FROM rooms WHERE id=$1", old.Room.Id).Scan(&roomExpired); err != nil || !roomExpired {
		t.Fatalf("old room expired=%v err=%v", roomExpired, err)
	}
	if err := db.QueryRow(ctx, "SELECT expires_at <= now() FROM room_invites WHERE room_id=$1", old.Room.Id).Scan(&inviteExpired); err != nil || !inviteExpired {
		t.Fatalf("old invite expired=%v err=%v", inviteExpired, err)
	}
	// A historical membership must not let the caller bypass the invariant
	// that they can participate in only one live room at a time.
	if _, err := db.Exec(ctx, "UPDATE rooms SET expires_at=now()+interval '1 hour' WHERE id=$1", old.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE room_invites SET expires_at=now()+interval '1 hour' WHERE room_id=$1", old.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, old.Invite.Token, "old-room-retry"); !errors.Is(err, rooms.ErrActiveRoomExists) {
		t.Fatalf("historical retry while another room is active error=%v; want active room exists", err)
	}

	failingUser := f.newUser(t)
	failingTarget := createJoinTarget(t, svc, f.third, f.city, "rollback-target")
	failing := newCreateService(t, db, failingRecorder{err: errors.New("behavior unavailable")})
	if _, err := failing.Join(ctx, contracts.Principal{UserID: failingUser}, failingTarget.Invite.Token, "rollback-join"); err == nil {
		t.Fatal("recorder failure unexpectedly committed")
	}
	for query, want := range map[string]int{
		"SELECT count(*) FROM room_members WHERE room_id=$1 AND user_id=$2":                           0,
		"SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND user_id=$2":                0,
		"SELECT count(*) FROM behavior_events WHERE room_id=$1 AND user_id=$2 AND type='room_joined'": 0,
	} {
		var got int
		if err := db.QueryRow(ctx, query, failingTarget.Room.Id, failingUser).Scan(&got); err != nil || got != want {
			t.Fatalf("rollback query %q=%d err=%v; want %d", query, got, err, want)
		}
	}
	var idempotencyRows int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM idempotency_records WHERE user_id=$1 AND key='rollback-join' AND route='/api/v1/room-invites/{token}/join'", failingUser).Scan(&idempotencyRows); err != nil || idempotencyRows != 0 {
		t.Fatalf("rollback idempotency rows=%d err=%v; want 0", idempotencyRows, err)
	}
	var consumed *uuid.UUID
	if err := db.QueryRow(ctx, "SELECT consumed_by FROM room_invites WHERE room_id=$1", failingTarget.Room.Id).Scan(&consumed); err != nil || consumed != nil {
		t.Fatalf("failed join consumed invite: %v err=%v", consumed, err)
	}
}

func TestJoinRoomConcurrencyEnforcesCapacityAndSameUserIdempotence(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	svc := newCreateService(t, db, behavior.Recorder{})
	ctx := context.Background()
	target := createJoinTarget(t, svc, f.creator, f.city, "concurrent-capacity")

	type result struct {
		response api.RoomSnapshot
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for i, user := range []uuid.UUID{f.member, f.third} {
		go func(i int, user uuid.UUID) {
			<-start
			response, err := svc.Join(ctx, contracts.Principal{UserID: user}, target.Invite.Token, "capacity-key-"+string(rune('a'+i)))
			results <- result{response, err}
		}(i, user)
	}
	close(start)
	var successes, full int
	for range 2 {
		result := <-results
		if result.err == nil {
			successes++
		} else if errors.Is(result.err, rooms.ErrRoomFull) {
			full++
		} else {
			t.Errorf("concurrent capacity join: %v", result.err)
		}
	}
	if successes != 1 || full != 1 {
		t.Fatalf("capacity results success=%d full=%d", successes, full)
	}
	var members, joins int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1", target.Room.Id).Scan(&members); err != nil || members != 2 {
		t.Fatalf("member count=%d err=%v; want 2", members, err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type='room_joined'", target.Room.Id).Scan(&joins); err != nil || joins != 1 {
		t.Fatalf("join behaviors=%d err=%v; want 1", joins, err)
	}

	creator, user := f.newUser(t), f.newUser(t)
	sameTarget := createJoinTarget(t, svc, creator, f.city, "concurrent-same-user")
	start = make(chan struct{})
	results = make(chan result, 2)
	for _, key := range []string{"same-user-one", "same-user-two"} {
		go func(key string) {
			<-start
			response, err := svc.Join(ctx, contracts.Principal{UserID: user}, sameTarget.Invite.Token, key)
			results <- result{response, err}
		}(key)
	}
	close(start)
	var responses []api.RoomSnapshot
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Errorf("same-user concurrent join: %v", result.err)
		} else {
			responses = append(responses, result.response)
		}
	}
	if len(responses) != 2 {
		t.Fatalf("same-user successes=%d; want 2", len(responses))
	}
	assertIdenticalJSON(t, responses[0], responses[1])
	assertJoinRows(t, db, sameTarget.Room.Id, user, 1, 1)
}

func createJoinTarget(t *testing.T, svc *rooms.Service, creator, city uuid.UUID, key string) api.CreateRoomResponse {
	t.Helper()
	response, err := svc.Create(context.Background(), contracts.Principal{UserID: creator}, key, api.CreateRoomRequest{CityId: city, Name: key})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertJoinRows(t *testing.T, db *store.Pool, roomID, userID uuid.UUID, memberships, behaviors int) {
	t.Helper()
	ctx := context.Background()
	checks := []struct {
		query string
		want  int
	}{
		{"SELECT count(*) FROM room_members WHERE room_id=$1 AND user_id=$2", memberships},
		{"SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND user_id=$2 AND round_no=1", memberships},
		{"SELECT count(*) FROM behavior_events WHERE room_id=$1 AND user_id=$2 AND type='room_joined'", behaviors},
	}
	for _, check := range checks {
		var got int
		if err := db.QueryRow(ctx, check.query, roomID, userID).Scan(&got); err != nil || got != check.want {
			t.Fatalf("%s=%d err=%v; want %d", check.query, got, err, check.want)
		}
	}
}
