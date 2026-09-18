package integration

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func TestRoomFoundationLocksAndMembershipGuards(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	f := newRoomFixture(t, db)

	if err := db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := roomsql.New(tx)
		if _, err := q.LockRoom(ctx, f.room); err != nil {
			return err
		}
		if _, err := q.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: f.room, UserID: f.creator, Role: "creator"}); err != nil {
			return err
		}
		_, err := q.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: f.room, UserID: f.member, Role: "participant"})
		return err
	}); err != nil {
		t.Fatalf("seed guarded members: %v", err)
	}

	// A third insert is guarded by the SELECT predicate rather than a handler-only check.
	_, err := roomsql.New(db).InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: f.room, UserID: f.third, Role: "participant"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("third member error = %v, want no rows", err)
	}
	if got, err := roomsql.New(db).CountRoomMembers(ctx, f.room); err != nil || got != 2 {
		t.Fatalf("member count = %d, %v; want 2, nil", got, err)
	}

	// The creator and active-membership indexes remain a backstop even for raw writes.
	_, err = db.Exec(ctx, "INSERT INTO room_members (room_id,user_id,role) VALUES ($1,$2,'creator')", f.room, f.third)
	assertPGCode(t, err, "23505")
	otherRoom := f.insertRoom(t, f.third)
	_, err = db.Exec(ctx, "INSERT INTO room_members (room_id,user_id,role) VALUES ($1,$2,'participant')", otherRoom, f.creator)
	assertPGCode(t, err, "23505")

	// Row locks must make competing room mutation fail deterministically, not race on sleep.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := roomsql.New(tx).LockRoom(ctx, f.room); err != nil {
		t.Fatal(err)
	}
	competing, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer competing.Release()
	if _, err := competing.Exec(ctx, "SET lock_timeout = '50ms'"); err != nil {
		t.Fatal(err)
	}
	_, err = competing.Exec(ctx, "SELECT id FROM rooms WHERE id=$1 FOR UPDATE", f.room)
	assertPGCode(t, err, "55P03")

	// Locking the durable users row serializes even the absence of an active membership.
	fourth := f.newUser(t)
	if _, err := roomsql.New(tx).LockMembershipUser(ctx, fourth); err != nil {
		t.Fatal(err)
	}
	_, err = competing.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE NOWAIT", fourth)
	assertPGCode(t, err, "55P03")

	capacityRoom := f.insertRoom(t, f.third)
	if err := db.InTx(ctx, pgx.TxOptions{}, func(seed pgx.Tx) error {
		_, err := roomsql.New(seed).InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: capacityRoom, UserID: f.third, Role: "creator"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	pendingMember, competingMember := f.newUser(t), f.newUser(t)
	capacityTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = capacityTx.Rollback(context.Background()) })
	if _, err := roomsql.New(capacityTx).LockRoom(ctx, capacityRoom); err != nil {
		t.Fatal(err)
	}
	if _, err := roomsql.New(capacityTx).InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: capacityRoom, UserID: pendingMember, Role: "participant"}); err != nil {
		t.Fatal(err)
	}
	_, err = competing.Exec(ctx, "INSERT INTO room_members (room_id,user_id,role) VALUES ($1,$2,'participant')", capacityRoom, competingMember)
	assertPGCode(t, err, "55P03")
	if err := capacityTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = roomsql.New(db).InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: capacityRoom, UserID: competingMember, Role: "participant"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("competing capacity retry = %v, want no rows", err)
	}

}

func TestRoomFoundationIntentReadinessAndPublicProjection(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	q := roomsql.New(db)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := q.InsertRoomRoundState(ctx, roomsql.InsertRoomRoundStateParams{RoomID: f.room, UserID: user, RoundNo: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := q.AreBothReady(ctx, roomsql.AreBothReadyParams{RoomID: f.room, RoundNo: 1}); err != nil || ready.Bool {
		t.Fatalf("one/two readiness initially = %+v, %v; want false", ready, err)
	}

	first, err := q.UpsertRoomIntent(ctx, intentParams(f, f.creator, "first", 1200))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetRoundReady(ctx, roomsql.SetRoundReadyParams{RoomID: f.room, UserID: f.creator, RoundNo: 1, Ready: true, IntentVersion: pgtype.Int4{Int32: first.Version, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if ready, err := q.AreBothReady(ctx, roomsql.AreBothReadyParams{RoomID: f.room, RoundNo: 1}); err != nil || ready.Bool {
		t.Fatalf("one ready = %+v, %v; want false", ready, err)
	}
	second, err := q.UpsertRoomIntent(ctx, intentParams(f, f.creator, "replacement", 2400))
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != first.Version+1 || second.FreeText.String != "replacement" || second.BudgetMaxMinor != 2400 {
		t.Fatalf("intent replacement = %+v; want new values and version increment", second)
	}
	if _, err := q.UpsertRoomIntent(ctx, intentParams(f, f.member, "private peer intent", 900)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetRoundReady(ctx, roomsql.SetRoundReadyParams{RoomID: f.room, UserID: f.member, RoundNo: 1, Ready: true, IntentVersion: pgtype.Int4{Int32: 1, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if ready, err := q.AreBothReady(ctx, roomsql.AreBothReadyParams{RoomID: f.room, RoundNo: 1}); err != nil || !ready.Bool {
		t.Fatalf("both ready = %+v, %v; want true", ready, err)
	}

	participants, err := q.GetPublicParticipants(ctx, roomsql.GetPublicParticipantsParams{RoomID: f.room, RoundNo: 1})
	if err != nil || len(participants) != 2 {
		t.Fatalf("public participants = %d, %v", len(participants), err)
	}
	publicType := reflect.TypeOf(roomsql.GetPublicParticipantsRow{})
	for _, forbidden := range []string{"MaxUserID", "DateOptions", "BudgetMaxMinor", "LocationLat", "LocationLng", "FreeText", "Intent"} {
		if _, ok := publicType.FieldByName(forbidden); ok {
			t.Errorf("public participant leaks %s", forbidden)
		}
	}
}

func TestRoomFoundationPoolVotesMatchAndAtomicRetirement(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	q := roomsql.New(db)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := q.InsertRoomRoundState(ctx, roomsql.InsertRoomRoundStateParams{RoomID: f.room, UserID: user, RoundNo: 1}); err != nil {
			t.Fatal(err)
		}
	}
	pool := uuid.New()
	if _, err := q.InsertRoomPool(ctx, roomsql.InsertRoomPoolParams{ID: pool, RoomID: f.room, Version: 1, RoundNo: 1, RankerVersion: "test", InputFingerprint: "pool-" + pool.String(), State: "ready", CandidateCount: 2, Diagnostics: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE rooms SET active_pool_version=1 WHERE id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	events := []uuid.UUID{f.newEvent(t), f.newEvent(t)}
	rows := []roomsql.InsertRoomPoolEventsParams{}
	for i, event := range events {
		rows = append(rows, roomsql.InsertRoomPoolEventsParams{PoolID: pool, EventID: event, Position: int32(i), Explanation: []byte(`{}`), FeatureSnapshot: []byte(`{}`)})
	}
	if got, err := q.InsertRoomPoolEvents(ctx, rows); err != nil || got != 2 {
		t.Fatalf("CopyFrom pool events = %d, %v", got, err)
	}
	gotEvents, err := q.GetRoomPoolEvents(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if gotEvents[0].EventID != events[0] || gotEvents[1].EventID != events[1] {
		t.Fatalf("pool order changed: %+v", gotEvents)
	}

	vote := roomsql.InsertRoomVoteParams{PoolID: pool, RoomID: f.room, EventID: events[0], UserID: f.creator, Vote: "like"}
	if n, err := q.InsertRoomVote(ctx, vote); err != nil || n != 1 {
		t.Fatalf("first vote = %d, %v", n, err)
	}
	vote.Vote = "dislike"
	if n, err := q.InsertRoomVote(ctx, vote); err != nil || n != 0 {
		t.Fatalf("duplicate vote = %d, %v", n, err)
	}
	stored, err := q.GetRoomVote(ctx, roomsql.GetRoomVoteParams{PoolID: pool, EventID: events[0], UserID: f.creator})
	if err != nil || stored.Vote != "like" {
		t.Fatalf("stored vote = %+v, %v; want immutable like", stored, err)
	}
	if n, err := q.InsertRoomVote(ctx, roomsql.InsertRoomVoteParams{PoolID: pool, RoomID: f.room, EventID: events[0], UserID: f.member, Vote: "like"}); err != nil || n != 1 {
		t.Fatalf("second vote = %d, %v", n, err)
	}
	if likes, err := q.CountPoolLikes(ctx, roomsql.CountPoolLikesParams{PoolID: pool, EventID: events[0]}); err != nil || likes != 2 {
		t.Fatalf("likes = %d, %v", likes, err)
	}
	if n, err := q.InsertRoomMatch(ctx, roomsql.InsertRoomMatchParams{ID: uuid.New(), RoomID: f.room, PoolID: pool, EventID: events[0]}); err != nil || n != 1 {
		t.Fatalf("first match = %d, %v", n, err)
	}
	if n, err := q.InsertRoomMatch(ctx, roomsql.InsertRoomMatchParams{ID: uuid.New(), RoomID: f.room, PoolID: pool, EventID: events[1]}); err != nil || n != 0 {
		t.Fatalf("duplicate match = %d, %v", n, err)
	}

	if _, err := q.MarkPoolFinished(ctx, roomsql.MarkPoolFinishedParams{RoomID: f.room, UserID: f.creator, RoundNo: 1}); err != nil {
		t.Fatal(err)
	}
	if done, err := q.AreBothPoolFinished(ctx, roomsql.AreBothPoolFinishedParams{RoomID: f.room, RoundNo: 1}); err != nil || done.Bool {
		t.Fatalf("one finished = %+v, %v; want false", done, err)
	}
	if _, err := q.MarkPoolFinished(ctx, roomsql.MarkPoolFinishedParams{RoomID: f.room, UserID: f.member, RoundNo: 1}); err != nil {
		t.Fatal(err)
	}
	if done, err := q.AreBothPoolFinished(ctx, roomsql.AreBothPoolFinishedParams{RoomID: f.room, RoundNo: 1}); err != nil || !done.Bool {
		t.Fatalf("both finished = %+v, %v; want true", done, err)
	}

	// Retirement and removal of coordinates commit together or neither does.
	if _, err := q.UpsertRoomIntent(ctx, intentParams(f, f.creator, "coords", 1)); err != nil {
		t.Fatal(err)
	}
	err = db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tq := roomsql.New(tx)
		if _, err := tq.RetireRoomMemberships(ctx, f.room); err != nil {
			return err
		}
		if _, err := tq.ClearRoomIntentCoordinates(ctx, f.room); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("rollback transaction unexpectedly succeeded")
	}
	var active bool
	var lat *float64
	if err := db.QueryRow(ctx, "SELECT is_active FROM room_members WHERE room_id=$1 AND user_id=$2", f.room, f.creator).Scan(&active); err != nil || !active {
		t.Fatalf("rollback active = %v, %v", active, err)
	}
	if err := db.QueryRow(ctx, "SELECT location_lat FROM room_intents WHERE room_id=$1 AND user_id=$2 AND round_no=1", f.room, f.creator).Scan(&lat); err != nil || lat == nil {
		t.Fatalf("rollback coordinate = %v, %v", lat, err)
	}
	if err := db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tq := roomsql.New(tx)
		if _, err := tq.RetireRoomMemberships(ctx, f.room); err != nil {
			return err
		}
		_, err := tq.ClearRoomIntentCoordinates(ctx, f.room)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT is_active FROM room_members WHERE room_id=$1 AND user_id=$2", f.room, f.creator).Scan(&active); err != nil || active {
		t.Fatalf("retired active = %v, %v", active, err)
	}
	if err := db.QueryRow(ctx, "SELECT location_lat FROM room_intents WHERE room_id=$1 AND user_id=$2 AND round_no=1", f.room, f.creator).Scan(&lat); err != nil || lat != nil {
		t.Fatalf("cleared coordinate = %v, %v", lat, err)
	}
	participants, err := q.GetPublicParticipants(ctx, roomsql.GetPublicParticipantsParams{RoomID: f.room, RoundNo: 1})
	if err != nil || len(participants) != 2 {
		t.Fatalf("historical participants after retirement = %d, %v; want 2, nil", len(participants), err)
	}
}

type roomFixture struct {
	db                                 roomsql.DBTX
	city, room, creator, member, third uuid.UUID
	seq                                int
}

var testMaxUserID = time.Now().UnixNano()

func newRoomFixture(t *testing.T, db *store.Pool) *roomFixture {
	t.Helper()
	f := &roomFixture{db: db, city: uuid.New(), room: uuid.New(), creator: uuid.New(), member: uuid.New(), third: uuid.New()}
	if _, err := db.Exec(context.Background(), "INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,$2,'UTC',0,0)", f.city, "test-"+f.city.String()); err != nil {
		t.Fatal(err)
	}
	for _, u := range []uuid.UUID{f.creator, f.member, f.third} {
		f.insertUser(t, u)
	}
	f.room = f.insertRoom(t, f.creator)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, statement := range []string{
			"DELETE FROM behavior_events WHERE room_id IN (SELECT id FROM rooms WHERE city_id=$1)",
			"DELETE FROM room_matches WHERE room_id IN (SELECT id FROM rooms WHERE city_id=$1)",
			"DELETE FROM rooms WHERE city_id=$1",
			"DELETE FROM events WHERE venue_id IN (SELECT id FROM venues WHERE city_id=$1)",
			"DELETE FROM venues WHERE city_id=$1",
			"DELETE FROM users WHERE city_id=$1",
			"DELETE FROM cities WHERE id=$1",
		} {
			if _, err := db.Exec(ctx, statement, f.city); err != nil {
				t.Errorf("fixture cleanup %q: %v", statement, err)
			}
		}
	})
	return f
}

func (f *roomFixture) insertUser(t *testing.T, id uuid.UUID) {
	t.Helper()
	f.seq++
	_, err := f.db.Exec(context.Background(), "INSERT INTO users (id,max_user_id,display_name,city_id) VALUES ($1,$2,$3,$4)", id, atomic.AddInt64(&testMaxUserID, 1), "user-"+id.String(), f.city)
	if err != nil {
		t.Fatal(err)
	}
}
func (f *roomFixture) newUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	f.insertUser(t, id)
	return id
}
func (f *roomFixture) insertRoom(t *testing.T, creator uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.db.Exec(context.Background(), "INSERT INTO rooms (id,creator_user_id,city_id,name,state,expires_at) VALUES ($1,$2,$3,'test','collecting_intents',now()+interval '1 day')", id, creator, f.city)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (f *roomFixture) addTwoMembers(t *testing.T) {
	t.Helper()
	q := roomsql.New(f.db)
	for _, p := range []roomsql.InsertRoomMemberParams{{RoomID: f.room, UserID: f.creator, Role: "creator"}, {RoomID: f.room, UserID: f.member, Role: "participant"}} {
		if _, err := q.InsertRoomMember(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
}
func (f *roomFixture) newEvent(t *testing.T) uuid.UUID {
	t.Helper()
	f.seq++
	venue := uuid.New()
	event := uuid.New()
	_, err := f.db.Exec(context.Background(), "INSERT INTO venues (id,city_id,name,address,latitude,longitude) VALUES ($1,$2,'v','a',0,0)", venue, f.city)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.db.Exec(context.Background(), "INSERT INTO events (id,source,external_id,title,description,venue_id,starts_at,timezone,status) VALUES ($1,'test',$2,'e','d',$3,now(),'UTC','published')", event, fmt.Sprintf("%d-%s", f.seq, event), venue)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
func intentParams(f *roomFixture, user uuid.UUID, text string, budget int32) roomsql.UpsertRoomIntentParams {
	d := pgtype.Date{Time: time.Now().AddDate(0, 0, 1), Valid: true}
	return roomsql.UpsertRoomIntentParams{RoomID: f.room, UserID: user, RoundNo: 1, DateOptions: []pgtype.Date{d}, DayTypes: []string{"weekend"}, TimeSlots: []string{"evening"}, CategorySlugs: []string{"music"}, BudgetMaxMinor: budget, LocationLat: pgtype.Float8{Float64: 55.7, Valid: true}, LocationLng: pgtype.Float8{Float64: 37.6, Valid: true}, RadiusM: pgtype.Int4{Int32: 1000, Valid: true}, ExclusionSlugs: []string{}, FreeText: pgtype.Text{String: text, Valid: true}}
}
func assertPGCode(t *testing.T, err error, want string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != want {
		t.Fatalf("pg error = %v; want SQLSTATE %s", err, want)
	}
}
