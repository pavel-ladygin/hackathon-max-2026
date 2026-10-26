package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func insertPreviewInvite(t *testing.T, f *roomFixture, expiresAt time.Time) (string, *rooms.Service) {
	t.Helper()
	codec, err := rooms.NewInviteCodec([]byte("0123456789abcdef0123456789abcdef"), 1, "https://app.test/invite/{token}", "https://max.test/bot?startapp={token}")
	if err != nil {
		t.Fatal(err)
	}
	material, err := codec.New(f.room, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roomsql.New(f.db).InsertRoomInvite(context.Background(), roomsql.InsertRoomInviteParams{
		ID: uuid.New(), RoomID: f.room, TokenHash: material.Hash, TokenCiphertext: material.Ciphertext,
		EncryptionKeyVersion: material.KeyVersion, CreatedBy: f.creator,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := rooms.NewCreateService(f.db.(*store.Pool), behavior.Recorder{}, codec, &poolBuilderFake{})
	if err != nil {
		t.Fatal(err)
	}
	return material.Token, svc
}

func TestCleanupExpiredRetiresMembersExpiresInvitesAndClearsCoordinates(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	ctx := context.Background()
	q := roomsql.New(db)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := q.InsertRoomRoundState(ctx, roomsql.InsertRoomRoundStateParams{RoomID: f.room, UserID: user, RoundNo: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := q.UpsertRoomIntent(ctx, intentParams(f, user, "sensitive", 100)); err != nil {
			t.Fatal(err)
		}
	}
	token, svc := insertPreviewInvite(t, f, time.Now().UTC().Add(time.Hour))
	if _, err := db.Exec(ctx, "UPDATE rooms SET expires_at=now()-interval '1 second' WHERE id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	cleaned, err := svc.CleanupExpired(ctx)
	if err != nil || cleaned != 1 {
		t.Fatalf("cleanup cleaned=%d err=%v; want 1/nil", cleaned, err)
	}
	var active, coordinates int
	var inviteExpired bool
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1 AND is_active", f.room).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_intents WHERE room_id=$1 AND (location_lat IS NOT NULL OR location_lng IS NOT NULL)", f.room).Scan(&coordinates); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT expires_at <= now() FROM room_invites WHERE room_id=$1", f.room).Scan(&inviteExpired); err != nil {
		t.Fatal(err)
	}
	if active != 0 || coordinates != 0 || !inviteExpired {
		t.Fatalf("cleanup active=%d coordinates=%d invite_expired=%v; want 0/0/true", active, coordinates, inviteExpired)
	}
	preview, err := svc.ResolveInviteContext(ctx, f.third, token)
	if err != nil || preview == nil || preview.Status != api.Expired {
		t.Fatalf("expired cleanup preview=%+v err=%v; want expired", preview, err)
	}
	if cleaned, err := svc.CleanupExpired(ctx); err != nil || cleaned != 0 {
		t.Fatalf("repeat cleanup cleaned=%d err=%v; want 0/nil", cleaned, err)
	}
}

func TestResolveInviteContextIsReadOnlyAndReportsJoinability(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	q := roomsql.New(db)
	if _, err := q.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: f.room, UserID: f.creator, Role: "creator"}); err != nil {
		t.Fatal(err)
	}
	token, svc := insertPreviewInvite(t, f, time.Now().UTC().Add(time.Hour))
	preview, err := svc.ResolveInviteContext(ctx, f.member, token)
	if err != nil || preview == nil || preview.Status != api.Joinable || preview.AlreadyJoined || preview.Token != token || preview.Inviter.Id != f.creator {
		t.Fatalf("joinable preview=%+v err=%v", preview, err)
	}
	var members int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1", f.room).Scan(&members); err != nil || members != 1 {
		t.Fatalf("preview changed membership count=%d err=%v; want 1", members, err)
	}
	if _, err := q.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: f.room, UserID: f.member, Role: "participant"}); err != nil {
		t.Fatal(err)
	}
	assertFullRoomPreview := func(t *testing.T, state string) {
		t.Helper()
		for _, tc := range []struct {
			name          string
			userID        uuid.UUID
			alreadyJoined bool
			status        api.InviteContextStatus
		}{
			{name: "creator", userID: f.creator, alreadyJoined: true, status: api.Joinable},
			{name: "member", userID: f.member, alreadyJoined: true, status: api.Joinable},
			{name: "outsider", userID: f.third, alreadyJoined: false, status: api.Full},
		} {
			t.Run(state+"/"+tc.name, func(t *testing.T) {
				got, err := svc.ResolveInviteContext(ctx, tc.userID, token)
				if err != nil || got == nil || got.AlreadyJoined != tc.alreadyJoined || got.Status != tc.status {
					t.Fatalf("preview=%+v err=%v; want already_joined=%v status=%s", got, err, tc.alreadyJoined, tc.status)
				}
			})
		}
	}
	assertFullRoomPreview(t, "collecting_intents")
	if _, err := db.Exec(ctx, "UPDATE room_members SET is_active=false WHERE room_id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	assertFullRoomPreview(t, "retired")
	for _, state := range []string{"matched", "exhausted"} {
		if _, err := db.Exec(ctx, "UPDATE rooms SET state=$2 WHERE id=$1", f.room, state); err != nil {
			t.Fatal(err)
		}
		assertFullRoomPreview(t, state)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1", f.room).Scan(&members); err != nil || members != 2 {
		t.Fatalf("previews changed membership count=%d err=%v; want 2", members, err)
	}
	if _, err := db.Exec(ctx, "UPDATE room_invites SET expires_at=now()-interval '1 second' WHERE room_id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []uuid.UUID{f.creator, f.third} {
		expired, err := svc.ResolveInviteContext(ctx, userID, token)
		if err != nil || expired == nil || expired.Status != api.Expired {
			t.Fatalf("expired preview=%+v err=%v; want expired", expired, err)
		}
	}
	unknown, err := svc.ResolveInviteContext(ctx, f.third, "unknown-invite-token-that-is-long-enough")
	if err != nil || unknown != nil {
		t.Fatalf("unknown preview=%+v err=%v; want nil/nil", unknown, err)
	}
}

func TestCleanupExpiredDeletesRetainedInviteSecretsForInactiveRoom(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	token, svc := insertPreviewInvite(t, f, time.Now().UTC().Add(-25*time.Hour))
	if token == "" {
		t.Fatal("fixture did not create invite token")
	}
	cleaned, err := svc.CleanupExpired(ctx)
	if err != nil || cleaned != 0 {
		t.Fatalf("inactive cleanup cleaned=%d err=%v; want 0/nil", cleaned, err)
	}
	var invites int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_invites WHERE room_id=$1", f.room).Scan(&invites); err != nil {
		t.Fatal(err)
	}
	if invites != 0 {
		t.Fatalf("retained expired invites=%d; want hash/ciphertext row deleted", invites)
	}
}

func TestCleanupExpiredDrainsMoreThanOneBatch(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	q := roomsql.New(db)
	const roomCount = 101
	for i := 0; i < roomCount; i++ {
		user := f.newUser(t)
		roomID := f.insertRoom(t, user)
		if _, err := q.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: roomID, UserID: user, Role: "creator"}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "UPDATE rooms SET expires_at=now()-interval '1 minute' WHERE id=$1", roomID); err != nil {
			t.Fatal(err)
		}
	}
	svc := rooms.NewService(db)
	cleaned, err := svc.CleanupExpired(ctx)
	if err != nil || cleaned < 0 || cleaned > roomCount {
		t.Fatalf("cleanup claimed=%d err=%v; want 0..%d/nil", cleaned, err, roomCount)
	}
	var active int
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = db.QueryRow(ctx, "SELECT count(*) FROM room_members m JOIN rooms r ON r.id=m.room_id WHERE r.city_id=$1 AND m.is_active", f.city).Scan(&active)
		if err != nil || active == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || active != 0 {
		t.Fatalf("active memberships after concurrent multi-batch cleanup=%d err=%v; want 0/nil (this worker claimed %d)", active, err, cleaned)
	}
}
