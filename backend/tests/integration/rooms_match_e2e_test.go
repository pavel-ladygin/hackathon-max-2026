package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// TestB9TwoClientMatchEndToEnd exercises the complete database-backed room
// lifecycle through the service boundary. Each iteration gets a fresh city,
// users, room and catalog event so the test can be repeated without relying on
// cleanup ordering or in-memory state.
func TestB9TwoClientMatchEndToEnd(t *testing.T) {
	db := openTestDB(t)
	for iteration := 0; iteration < 10; iteration++ {
		t.Run(fmt.Sprintf("iteration-%02d", iteration+1), func(t *testing.T) {
			ctx := context.Background()
			f := newRoomFixture(t, db)

			// newRoomFixture creates a room for convenience. Expire it so the
			// production Create path can retire its membership and create the
			// room used by this end-to-end scenario.
			if _, err := db.Exec(ctx, "UPDATE rooms SET expires_at=now()-interval '1 minute' WHERE id=$1", f.room); err != nil {
				t.Fatal(err)
			}

			event := f.newEvent(t)
			if _, err := db.Exec(ctx, "UPDATE events SET price_from_minor=100,currency='RUB',ticket_url='https://tickets.example/b9',ticket_available=true WHERE id=$1", event); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "INSERT INTO event_categories (event_id,category_slug,is_primary) VALUES ($1,'music',true)", event); err != nil {
				t.Fatal(err)
			}

			builder := &poolBuilderFake{result: contracts.BuildResult{
				Candidates:    []contracts.Candidate{{EventID: event}},
				RankerVersion: "b9-e2e", InputFingerprint: uuid.NewString(),
			}}
			svc := newCreateServiceWithBuilder(t, db, behavior.Recorder{}, builder)
			codec, err := rooms.NewRoomEventsCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.EnableRoomEvents(catalog.NewRepository(db), codec); err != nil {
				t.Fatal(err)
			}

			created, err := svc.Create(ctx, contracts.Principal{UserID: f.creator}, fmt.Sprintf("b9-create-%d", iteration), api.CreateRoomRequest{CityId: f.city, Name: "B9 room"})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			f.room = created.Room.Id
			if created.Invite.Token == "" {
				t.Fatal("create returned an empty invite token")
			}

			joined, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, fmt.Sprintf("b9-join-%d", iteration))
			if err != nil {
				t.Fatalf("join: %v", err)
			}
			if joined.Id != f.room || len(joined.Participants) != 2 {
				t.Fatalf("join snapshot=%+v; want room %s with two participants", joined, f.room)
			}

			for _, user := range []uuid.UUID{f.creator, f.member} {
				snapshot, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: user}, f.room, validIntentRequest())
				if err != nil {
					t.Fatalf("replace intent user %s: %v", user, err)
				}
				if user == f.creator && transitioned {
					t.Fatal("creator intent transitioned before the second client was ready")
				}
				if user == f.member && (!transitioned || snapshot.State != api.RoomStateVoting) {
					t.Fatalf("member intent snapshot=%+v transitioned=%v; want voting transition", snapshot, transitioned)
				}
			}

			voteRequest := api.VoteRequest{PoolVersion: 1, Vote: api.Like}
			if _, err := svc.Vote(ctx, contracts.Principal{UserID: f.creator}, f.room, event, voteRequest); err != nil {
				t.Fatalf("creator like: %v", err)
			}
			matchedResponse, err := svc.Vote(ctx, contracts.Principal{UserID: f.member}, f.room, event, voteRequest)
			if err != nil {
				t.Fatalf("member like/match: %v", err)
			}
			match, err := matchedResponse.Match.Get()
			if err != nil || match.Event.Id != event || match.Id == uuid.Nil || len(match.Participants) != 2 {
				t.Fatalf("match response=%+v err=%v; want the selected event and both participants", match, err)
			}

			for _, user := range []uuid.UUID{f.creator, f.member} {
				reloaded, err := svc.Get(ctx, contracts.Principal{UserID: user}, f.room)
				if err != nil {
					t.Fatalf("reload user %s: %v", user, err)
				}
				reloadedMatch, err := reloaded.Match.Get()
				if err != nil || reloadedMatch.Id != match.Id || reloadedMatch.EventId != event {
					t.Fatalf("reload user %s match=%+v err=%v; want %s", user, reloadedMatch, err, match.Id)
				}
				if reloaded.State != api.RoomStateMatched || len(reloaded.AllowedActions) != 1 || reloaded.AllowedActions[0] != api.ViewMatch {
					t.Fatalf("reload user %s state=%s actions=%v; want matched with view_match", user, reloaded.State, reloaded.AllowedActions)
				}
			}

			retried, err := svc.Vote(ctx, contracts.Principal{UserID: f.member}, f.room, event, voteRequest)
			if err != nil {
				t.Fatalf("terminal vote retry: %v", err)
			}
			retriedMatch, err := retried.Match.Get()
			if err != nil || retriedMatch.Id != match.Id {
				t.Fatalf("terminal retry match=%+v err=%v; want %s", retriedMatch, err, match.Id)
			}

			var matches, terminalEvents, activeMembers, coordinates int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM room_matches WHERE room_id=$1", f.room).Scan(&matches); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type='match'", f.room).Scan(&terminalEvents); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, "SELECT count(*) FROM room_members WHERE room_id=$1 AND is_active", f.room).Scan(&activeMembers); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, "SELECT count(*) FROM room_intents WHERE room_id=$1 AND (location_lat IS NOT NULL OR location_lng IS NOT NULL)", f.room).Scan(&coordinates); err != nil {
				t.Fatal(err)
			}
			if matches != 1 || terminalEvents != 1 || activeMembers != 0 || coordinates != 0 {
				t.Fatalf("terminal persistence matches=%d terminal_events=%d active_members=%d coordinates=%d", matches, terminalEvents, activeMembers, coordinates)
			}
		})
	}
}

// newCreateServiceWithBuilder mirrors newCreateService while allowing this
// test to supply the deterministic one-event pool used by the E2E flow.
func newCreateServiceWithBuilder(t *testing.T, db *store.Pool, recorder contracts.BehaviorRecorder, builder contracts.PoolBuilder) *rooms.Service {
	t.Helper()
	invites, err := rooms.NewInviteCodec([]byte("0123456789abcdef0123456789abcdef"), 1, "https://app.test/invite/{token}", "https://max.test/bot?startapp={token}")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := rooms.NewCreateService(db, recorder, invites, builder)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}
