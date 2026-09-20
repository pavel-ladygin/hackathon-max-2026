package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func TestGetRoomReturnsCallerScopedRecoverySnapshot(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	svc := newCreateService(t, db, behavior.Recorder{})
	created := createJoinTarget(t, svc, f.creator, f.city, "get-room")
	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "get-room-join"); err != nil {
		t.Fatal(err)
	}

	q := roomsql.New(db)
	creatorIntent := intentParams(f, f.creator, "creator-visible", 2200)
	creatorIntent.RoomID = created.Room.Id
	creatorIntent.LocationLat = pgtype.Float8{}
	creatorIntent.LocationLng = pgtype.Float8{}
	creatorIntent.RadiusM = pgtype.Int4{}
	creatorIntent.FreeText = pgtype.Text{}
	creatorStored, err := q.UpsertRoomIntent(ctx, creatorIntent)
	if err != nil {
		t.Fatal(err)
	}
	memberIntent := intentParams(f, f.member, "peer-private-marker", 9900)
	memberIntent.RoomID = created.Room.Id
	memberStored, err := q.UpsertRoomIntent(ctx, memberIntent)
	if err != nil {
		t.Fatal(err)
	}
	for _, ready := range []struct {
		user    uuid.UUID
		version int32
	}{{f.creator, creatorStored.Version}, {f.member, memberStored.Version}} {
		if _, err := q.SetRoundReady(ctx, roomsql.SetRoundReadyParams{RoomID: created.Room.Id, UserID: ready.user, RoundNo: 1, Ready: true, IntentVersion: pgtype.Int4{Int32: ready.version, Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}

	creator, err := svc.Get(ctx, contracts.Principal{UserID: f.creator}, created.Room.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(creator.Participants) != 2 || !creator.Participants[0].IntentReady || !creator.Participants[1].IntentReady {
		t.Fatalf("unsafe/incomplete participants: %+v", creator.Participants)
	}
	invite, err := creator.Invite.Get()
	if err != nil || invite.Url == "" || invite.MaxDeepLink == "" {
		t.Fatalf("creator invite = %+v, %v", invite, err)
	}
	intent, err := creator.MyIntent.Get()
	if err != nil || intent.BudgetMaxMinor != 2200 || !nullableIsNull(intent.Location) || !nullableIsNull(intent.RadiusM) || !nullableIsNull(intent.FreeText) {
		t.Fatalf("creator intent nullable mapping = %+v, %v", intent, err)
	}
	raw, err := json.Marshal(creator)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"peer-private-marker", "9900", "token_hash", "token_ciphertext", "max_user_id", "location_lat", "location_lng"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("creator snapshot leaks %q: %s", forbidden, raw)
		}
	}

	participant, err := svc.Get(ctx, contracts.Principal{UserID: f.member}, created.Room.Id)
	if err != nil {
		t.Fatal(err)
	}
	if !nullableIsNull(participant.Invite) {
		t.Fatalf("participant invite = %+v; want null", participant.Invite)
	}
	participantIntent, err := participant.MyIntent.Get()
	participantText, textErr := participantIntent.FreeText.Get()
	if err != nil || textErr != nil || participantText != "peer-private-marker" || participantIntent.BudgetMaxMinor != 9900 {
		t.Fatalf("participant own intent = %+v, %v", participantIntent, err)
	}

	if _, err := svc.Get(ctx, contracts.Principal{UserID: f.third}, created.Room.Id); !errors.Is(err, rooms.ErrRoomNotFound) {
		t.Fatalf("outsider error = %v; want room not found", err)
	}
	if _, err := svc.Get(ctx, contracts.Principal{UserID: f.creator}, uuid.New()); !errors.Is(err, rooms.ErrRoomNotFound) {
		t.Fatalf("missing room error = %v; want room not found", err)
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET expires_at=now()-interval '1 second' WHERE id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, contracts.Principal{UserID: f.creator}, created.Room.Id); !errors.Is(err, rooms.ErrRoomNotFound) {
		t.Fatalf("expired room error = %v; want room not found", err)
	}
}

func TestGetRoomPoolMatchAndAllowedActions(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	svc := newCreateService(t, db, behavior.Recorder{})
	created := createJoinTarget(t, svc, f.creator, f.city, "get-lifecycle")
	if _, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "get-lifecycle-join"); err != nil {
		t.Fatal(err)
	}

	assertActions := func(user uuid.UUID, want ...api.RoomSnapshotAllowedActions) api.RoomSnapshot {
		t.Helper()
		got, err := svc.Get(ctx, contracts.Principal{UserID: user}, created.Room.Id)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.AllowedActions) != len(want) {
			t.Fatalf("actions = %v; want %v", got.AllowedActions, want)
		}
		for i := range want {
			if got.AllowedActions[i] != want[i] {
				t.Fatalf("actions = %v; want %v", got.AllowedActions, want)
			}
		}
		return got
	}

	assertActions(f.creator, api.EditIntent)
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='ranking' WHERE id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	assertActions(f.creator, api.Wait)

	poolID, eventID := uuid.New(), f.newEvent(t)
	q := roomsql.New(db)
	if _, err := q.InsertRoomPool(ctx, roomsql.InsertRoomPoolParams{ID: poolID, RoomID: created.Room.Id, Version: 1, RoundNo: 1, RankerVersion: "test", InputFingerprint: "get-" + poolID.String(), State: "ready", CandidateCount: 2, IsSmall: true, Diagnostics: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertRoomPoolEvents(ctx, []roomsql.InsertRoomPoolEventsParams{{PoolID: poolID, EventID: eventID, Position: 0, Explanation: []byte(`{}`), FeatureSnapshot: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='voting', active_pool_version=1 WHERE id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertRoomVote(ctx, roomsql.InsertRoomVoteParams{PoolID: poolID, RoomID: created.Room.Id, EventID: eventID, UserID: f.creator, Vote: "dislike"}); err != nil {
		t.Fatal(err)
	}
	voting := assertActions(f.creator, api.ViewPool, api.Vote)
	pool, err := voting.Pool.Get()
	if err != nil || pool.Version != 1 || pool.RoundNo != 1 || pool.Total != 2 || !pool.IsSmall || pool.VotedByMe != 1 || pool.MyPoolFinished || pool.RoomExhausted {
		t.Fatalf("pool summary = %+v, %v", pool, err)
	}
	if _, err := q.MarkPoolFinished(ctx, roomsql.MarkPoolFinishedParams{RoomID: created.Room.Id, UserID: f.creator, RoundNo: 1}); err != nil {
		t.Fatal(err)
	}
	finished := assertActions(f.creator, api.ViewPool, api.Wait)
	pool, _ = finished.Pool.Get()
	if !pool.MyPoolFinished || pool.RoomExhausted {
		t.Fatalf("one-user finished pool summary = %+v", pool)
	}
	if _, err := q.MarkPoolFinished(ctx, roomsql.MarkPoolFinishedParams{RoomID: created.Room.Id, UserID: f.member, RoundNo: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='exhausted' WHERE id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	exhausted := assertActions(f.creator, api.RestartWithNewIntent)
	pool, _ = exhausted.Pool.Get()
	if !pool.RoomExhausted {
		t.Fatalf("exhausted pool summary = %+v", pool)
	}

	matchID := uuid.New()
	if _, err := q.InsertRoomMatch(ctx, roomsql.InsertRoomMatchParams{ID: matchID, RoomID: created.Room.Id, PoolID: poolID, EventID: eventID}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='matched' WHERE id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE room_members SET is_active=false WHERE room_id=$1", created.Room.Id); err != nil {
		t.Fatal(err)
	}
	matched := assertActions(f.member, api.ViewMatch)
	match, err := matched.Match.Get()
	if err != nil || match.Id != matchID || match.RoomId != created.Room.Id || match.EventId != eventID || len(match.Participants) != 2 {
		t.Fatalf("match summary = %+v, %v", match, err)
	}
	raw, err := json.Marshal(match)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"\"max_user_id\":", "\"intent\":", "\"vote\":"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("match summary leaks %q: %s", forbidden, raw)
		}
	}
}
