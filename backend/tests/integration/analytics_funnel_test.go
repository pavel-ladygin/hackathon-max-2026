package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This exercises the SQL-backed room facts used by the dashboard. Raw client
// telemetry is deliberately duplicated while the expected funnel remains one
// row per room.
func TestRoomFunnelUsesRoomFactsAndCreationCohort(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	now := databaseNow(t, db)

	// Invite-link opens and reloads are telemetry only. A consumed invite plus
	// a successful distinct-member join confirms the invite stage once.
	if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role,joined_at)
		VALUES ($1,$2,'creator',$3),($1,$4,'participant',$3)`, f.room, f.creator, now, f.member); err != nil {
		t.Fatal(err)
	}
	inviteTokenHash := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO room_invites(id,room_id,token_hash,token_ciphertext,encryption_key_version,created_by,expires_at,consumed_by,consumed_at)
		VALUES ($1,$2,$3,$4,1,$5,$6,$7,$6)`, uuid.New(), f.room, inviteTokenHash[:], []byte(uuid.NewString()), f.creator, now.Add(time.Hour), f.member); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := db.Exec(ctx, `INSERT INTO behavior_events(id,user_id,origin,type,room_id,client_event_id,occurred_at)
			VALUES ($1,$2,'client','invite_link_opened',$3,$4,$5)`, uuid.New(), f.member, f.room, uuid.New().String(), now); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate repeat join and reload requests: room_members' primary key
	// leaves a single distinct participant and the funnel reads that fact.
	if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role,joined_at)
		VALUES ($1,$2,'participant',$3) ON CONFLICT(room_id,user_id) DO NOTHING`, f.room, f.member, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	event := f.newEvent(t)
	pool := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO room_pools(id,room_id,version,round_no,ranker_version,input_fingerprint,state,candidate_count,created_at)
		VALUES ($1,$2,1,1,'test','analytics-funnel','ready',1,$3)`, pool, f.room, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO room_pool_events(pool_id,event_id,position,group_score,participant_score_min,participant_score_mean,explanation,feature_snapshot)
		VALUES ($1,$2,0,1,1,1,'{}','{}')`, pool, event); err != nil {
		t.Fatal(err)
	}
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := db.Exec(ctx, `INSERT INTO room_votes(pool_id,room_id,event_id,user_id,vote,created_at)
			VALUES ($1,$2,$3,$4,'like',$5)`, pool, f.room, event, user, now.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO room_matches(id,room_id,pool_id,event_id,matched_at)
		VALUES ($1,$2,$3,$4,$5)`, uuid.New(), f.room, pool, event, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Repeated match and ticket events must not multiply room conversion; raw
	// ticket_clicks may still report the two clicks separately.
	for i := 0; i < 2; i++ {
		for _, typ := range []string{"match", "match_opened", "ticket_click"} {
			if _, err := db.Exec(ctx, `INSERT INTO behavior_events(id,user_id,origin,type,event_id,room_id,occurred_at)
				VALUES ($1,$2,'server',$3,$4,$5,$6)`, uuid.New(), f.member, typ, event, f.room, now.Add(4*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
	}

	var inviteConfirmed, joined, activated, matched, ticketClicked, matchEventOpened bool
	var ticketClicks, swipes int
	err := db.QueryRow(ctx, `SELECT invite_confirmed, joined, activated, matched, ticket_clicked, match_event_opened, ticket_clicks, swipe_count
		FROM analytics_room_funnel_metrics WHERE room_id=$1`, f.room).
		Scan(&inviteConfirmed, &joined, &activated, &matched, &ticketClicked, &matchEventOpened, &ticketClicks, &swipes)
	if err != nil {
		t.Fatalf("read funnel room facts: %v", err)
	}
	if !inviteConfirmed || !joined || !activated || !matched || !ticketClicked || !matchEventOpened {
		t.Fatalf("room stages = invite:%v join:%v activate:%v match:%v event-open:%v ticket:%v; want all true once", inviteConfirmed, joined, activated, matched, matchEventOpened, ticketClicked)
	}
	if ticketClicks != 2 {
		t.Fatalf("raw ticket_clicks=%d, want 2", ticketClicks)
	}
	if swipes != 1 {
		t.Fatalf("unique event swipes=%d, want 1", swipes)
	}

	// A second room can have a successful join without an observed invite
	// action. The raw facts would produce joined > invite; the funnel's
	// sequential aggregate must cap joined at the invite-confirmed cohort.
	uninvitedCreator, uninvitedMember := f.newUser(t), f.newUser(t)
	uninvitedRoom := f.insertRoom(t, uninvitedCreator)
	if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role,joined_at)
		VALUES ($1,$2,'creator',$3),($1,$4,'participant',$3)`, uninvitedRoom, uninvitedCreator, now, uninvitedMember); err != nil {
		t.Fatal(err)
	}
	var createdCount, inviteCount, rawJoinCount, funnelJoinCount int
	if err := db.QueryRow(ctx, `SELECT count(*)::int,
		count(*) FILTER (WHERE invite_confirmed)::int,
		count(*) FILTER (WHERE joined)::int,
		count(*) FILTER (WHERE invite_confirmed AND joined)::int
		FROM analytics_room_funnel_metrics WHERE creator_user_id IN ($1,$2) AND created_at >= $3`,
		f.creator, uninvitedCreator, now.Add(-30*24*time.Hour)).
		Scan(&createdCount, &inviteCount, &rawJoinCount, &funnelJoinCount); err != nil {
		t.Fatal(err)
	}
	if createdCount != 2 || inviteCount != 1 || rawJoinCount != 2 || funnelJoinCount != 1 {
		t.Fatalf("cohort raw stages created=%d invite=%d join=%d, sequential join=%d; want 2,1,2,1", createdCount, inviteCount, rawJoinCount, funnelJoinCount)
	}

	// A room created before the selected period stays out of the cohort even
	// when a participant joins during that period.
	oldCreator := f.newUser(t)
	oldRoom := f.insertRoom(t, oldCreator)
	if _, err := db.Exec(ctx, `UPDATE rooms SET created_at=$2 WHERE id=$1`, oldRoom, now.Add(-120*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO room_members(room_id,user_id,role,joined_at) VALUES ($1,$2,'creator',$3),($1,$4,'participant',$3)`, oldRoom, oldCreator, now, f.third); err != nil {
		t.Fatal(err)
	}
	var oldRoomCount int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM analytics_room_funnel_metrics
		WHERE creator_user_id=$1 AND created_at >= $2 AND joined`, f.creator, now.Add(-30*24*time.Hour)).Scan(&oldRoomCount); err != nil {
		t.Fatal(err)
	}
	if oldRoomCount != 1 {
		t.Fatalf("joined rooms in creation cohort=%d, want only current room", oldRoomCount)
	}
}
