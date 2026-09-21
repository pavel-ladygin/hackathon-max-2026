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
	beforeMembers := 1
	preview, err := svc.ResolveInviteContext(ctx, f.member, token)
	if err != nil || preview == nil || preview.Status != api.Joinable || preview.AlreadyJoined || preview.Token != token || preview.Inviter.Id != f.creator {
		t.Fatalf("joinable preview=%+v err=%v", preview, err)
	}
	var members int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1", f.room).Scan(&members); err != nil || members != beforeMembers {
		t.Fatalf("preview changed membership count=%d err=%v; want %d", members, err, beforeMembers)
	}
	if _, err := q.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: f.room, UserID: f.member, Role: "participant"}); err != nil {
		t.Fatal(err)
	}
	alreadyJoined, err := svc.ResolveInviteContext(ctx, f.member, token)
	if err != nil || alreadyJoined == nil || !alreadyJoined.AlreadyJoined || alreadyJoined.Status != api.Joinable {
		t.Fatalf("member preview=%+v err=%v; want already joined/joinable", alreadyJoined, err)
	}
	full, err := svc.ResolveInviteContext(ctx, f.third, token)
	if err != nil || full == nil || full.AlreadyJoined || full.Status != api.Full {
		t.Fatalf("full preview=%+v err=%v; want full", full, err)
	}
	if _, err := db.Exec(ctx, "UPDATE room_members SET is_active=false WHERE room_id=$1 AND user_id=$2", f.room, f.member); err != nil {
		t.Fatal(err)
	}
	inactive, err := svc.ResolveInviteContext(ctx, f.member, token)
	if err != nil || inactive == nil || inactive.AlreadyJoined || inactive.Status != api.Joinable {
		t.Fatalf("inactive member preview=%+v err=%v; want not joined/joinable", inactive, err)
	}
	unknown, err := svc.ResolveInviteContext(ctx, f.third, "unknown-invite-token-that-is-long-enough")
	if err != nil || unknown != nil {
		t.Fatalf("unknown preview=%+v err=%v; want nil/nil", unknown, err)
	}
}
