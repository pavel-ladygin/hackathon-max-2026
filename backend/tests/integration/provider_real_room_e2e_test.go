package integration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/saved"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/tickets"
)

type realRoomEvent struct {
	id       uuid.UUID
	source   string
	startsAt time.Time
	category string
}

// TestProviderRealRoomEndToEnd repeats the complete two-user room flow with
// ten distinct imported occurrences split across KudaGo and Timepad.
func TestProviderRealRoomEndToEnd(t *testing.T) {
	if os.Getenv("RUN_REAL_PROVIDER_ROOM_E2E") != "1" {
		t.Skip("set RUN_REAL_PROVIDER_ROOM_E2E=1 to use an imported real provider catalog")
	}
	db := openTestDB(t)
	ctx := context.Background()
	cityID := uuid.MustParse(catalogseed.MoscowCityID)

	rows, err := db.Query(ctx, `WITH eligible AS (
		SELECT e.id,e.source,e.starts_at,ec.category_slug,
		       row_number() OVER (PARTITION BY e.source ORDER BY e.starts_at,e.id) AS source_position
		FROM events e
		JOIN venues v ON v.id=e.venue_id
		JOIN event_categories ec ON ec.event_id=e.id AND ec.is_primary
		WHERE v.city_id=$1 AND e.source IN ('kudago','timepad')
		  AND e.is_demo=false AND e.provider_active=true AND e.status='published'
		  AND e.starts_at>now() AND e.price_from_minor IS NOT NULL
		  AND e.ticket_available=true AND e.ticket_url LIKE 'https://%'
	)
	SELECT id,source,starts_at,category_slug FROM eligible
	WHERE source_position<=5 ORDER BY source_position,source`, cityID)
	if err != nil {
		t.Fatal(err)
	}
	var events []realRoomEvent
	for rows.Next() {
		var event realRoomEvent
		if err := rows.Scan(&event.id, &event.source, &event.startsAt, &event.category); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 10 {
		t.Fatalf("eligible real events=%d, want five from each provider", len(events))
	}

	zone, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	for iteration, event := range events {
		event := event
		t.Run(fmt.Sprintf("%02d-%s-%s", iteration+1, event.source, event.id), func(t *testing.T) {
			firstUser, secondUser := uuid.New(), uuid.New()
			for _, user := range []uuid.UUID{firstUser, secondUser} {
				if _, err := db.Exec(ctx, "INSERT INTO users(id,max_user_id,display_name,city_id) VALUES($1,$2,$3,$4)", user, atomic.AddInt64(&testMaxUserID, 1), "provider-e2e-"+user.String(), cityID); err != nil {
					t.Fatal(err)
				}
			}
			var roomID uuid.UUID
			t.Cleanup(func() {
				_, _ = db.Exec(context.Background(), "UPDATE events SET provider_active=true WHERE id=$1", event.id)
				_, _ = db.Exec(context.Background(), "DELETE FROM behavior_events WHERE user_id=ANY($1)", []uuid.UUID{firstUser, secondUser})
				_, _ = db.Exec(context.Background(), "DELETE FROM saved_events WHERE user_id=ANY($1)", []uuid.UUID{firstUser, secondUser})
				if roomID != uuid.Nil {
					_, _ = db.Exec(context.Background(), "DELETE FROM room_matches WHERE room_id=$1", roomID)
					_, _ = db.Exec(context.Background(), "DELETE FROM rooms WHERE id=$1", roomID)
				}
				_, _ = db.Exec(context.Background(), "DELETE FROM idempotency_records WHERE user_id=ANY($1)", []uuid.UUID{firstUser, secondUser})
				_, _ = db.Exec(context.Background(), "DELETE FROM users WHERE id=ANY($1)", []uuid.UUID{firstUser, secondUser})
			})

			builder := &poolBuilderFake{result: contracts.BuildResult{
				Candidates: []contracts.Candidate{{EventID: event.id}}, RankerVersion: "provider-real-e2e", InputFingerprint: event.id.String(),
			}}
			svc := newCreateServiceWithBuilder(t, db, behavior.Recorder{}, builder)
			codec, err := rooms.NewRoomEventsCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.EnableRoomEvents(catalog.NewRepository(db), codec); err != nil {
				t.Fatal(err)
			}
			created, err := svc.Create(ctx, contracts.Principal{UserID: firstUser}, fmt.Sprintf("provider-real-create-%d", iteration), api.CreateRoomRequest{CityId: cityID, Name: "Provider real E2E"})
			if err != nil {
				t.Fatal(err)
			}
			roomID = created.Room.Id
			if _, err := svc.Join(ctx, contracts.Principal{UserID: secondUser}, created.Invite.Token, fmt.Sprintf("provider-real-join-%d", iteration)); err != nil {
				t.Fatal(err)
			}
			request := api.RoomIntentRequest{
				Dates: []openapi_types.Date{{Time: event.startsAt.In(zone)}}, CategorySlugs: []api.CategorySlug{api.CategorySlug(event.category)},
				BudgetMaxMinor: 100_000_000, DayTypes: []api.DayType{}, TimeSlots: []api.TimeSlot{},
				ExclusionSlugs: []api.RoomIntentRequestExclusionSlugs{}, Location: nullable.NewNullNullable[api.GeoPoint](), RadiusM: nullable.NewNullNullable[int](),
			}
			if _, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: firstUser}, roomID, request); err != nil || transitioned {
				t.Fatalf("first intent transitioned=%t err=%v", transitioned, err)
			}
			if _, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: secondUser}, roomID, request); err != nil || !transitioned {
				t.Fatalf("second intent transitioned=%t err=%v", transitioned, err)
			}

			firstCards, err := svc.GetEvents(ctx, contracts.Principal{UserID: firstUser}, roomID, rooms.RoomEventsInput{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			secondCards, err := svc.GetEvents(ctx, contracts.Principal{UserID: secondUser}, roomID, rooms.RoomEventsInput{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			if len(firstCards.Items) != 1 || len(secondCards.Items) != 1 || !reflect.DeepEqual(firstCards.Items[0].Event.Id, secondCards.Items[0].Event.Id) || firstCards.Items[0].Event.Id != event.id {
				t.Fatalf("different room card order first=%v second=%v", firstCards.Items, secondCards.Items)
			}
			var poolSnapshot string
			if err := db.QueryRow(ctx, `SELECT row_to_json(rpe)::text FROM room_pool_events rpe JOIN room_pools rp ON rp.id=rpe.pool_id WHERE rp.room_id=$1 AND rpe.event_id=$2`, roomID, event.id).Scan(&poolSnapshot); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "UPDATE events SET provider_active=false WHERE id=$1", event.id); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Vote(ctx, contracts.Principal{UserID: firstUser}, roomID, event.id, api.VoteRequest{PoolVersion: 1, Vote: api.Like}); !errors.Is(err, rooms.ErrEventUnavailable) {
				t.Fatalf("inactive event vote err=%v", err)
			}
			var snapshotAfter string
			if err := db.QueryRow(ctx, `SELECT row_to_json(rpe)::text FROM room_pool_events rpe JOIN room_pools rp ON rp.id=rpe.pool_id WHERE rp.room_id=$1 AND rpe.event_id=$2`, roomID, event.id).Scan(&snapshotAfter); err != nil || snapshotAfter != poolSnapshot {
				t.Fatalf("immutable pool changed after reconciliation fixture err=%v", err)
			}
			if _, err := db.Exec(ctx, "UPDATE events SET provider_active=true WHERE id=$1", event.id); err != nil {
				t.Fatal(err)
			}

			vote := api.VoteRequest{PoolVersion: 1, Vote: api.Like}
			if _, err := svc.Vote(ctx, contracts.Principal{UserID: firstUser}, roomID, event.id, vote); err != nil {
				t.Fatal(err)
			}
			matched, err := svc.Vote(ctx, contracts.Principal{UserID: secondUser}, roomID, event.id, vote)
			if err != nil {
				t.Fatal(err)
			}
			match, err := matched.Match.Get()
			if err != nil || match.Event.Id != event.id {
				t.Fatalf("match=%+v err=%v", match, err)
			}
			var matchCount int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM room_matches WHERE room_id=$1", roomID).Scan(&matchCount); err != nil || matchCount != 1 {
				t.Fatalf("persisted matches=%d err=%v", matchCount, err)
			}

			savedService, err := saved.NewService(db, behavior.Recorder{}, []byte("0123456789abcdef0123456789abcdef"))
			if err != nil {
				t.Fatal(err)
			}
			if state, err := savedService.Set(ctx, firstUser, event.id, true); err != nil || !state.Saved {
				t.Fatalf("save state=%+v err=%v", state, err)
			}
			ticketService, err := tickets.NewService(db, behavior.Recorder{}, []string{"kudago.com", "*.kudago.com", "timepad.ru", "*.timepad.ru"})
			if err != nil {
				t.Fatal(err)
			}
			cta, err := ticketService.Click(ctx, firstUser, event.id)
			parsed, parseErr := url.Parse(cta)
			if err != nil || parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" {
				t.Fatalf("real CTA=%q click_err=%v parse_err=%v", cta, err, parseErr)
			}

			if _, err := db.Exec(ctx, "UPDATE events SET provider_active=false WHERE id=$1", event.id); err != nil {
				t.Fatal(err)
			}
			savedPage, err := savedService.List(ctx, firstUser, saved.ListInput{Tab: saved.TabSaved, Limit: 20})
			if err != nil || len(savedPage.Items) != 1 || savedPage.Items[0].Event.ID != event.id {
				t.Fatalf("saved history after deactivation=%+v err=%v", savedPage, err)
			}
			for _, user := range []uuid.UUID{firstUser, secondUser} {
				matchPage, err := savedService.List(ctx, user, saved.ListInput{Tab: saved.TabMatches, Limit: 20})
				if err != nil || len(matchPage.Items) != 1 || matchPage.Items[0].Event.ID != event.id {
					t.Fatalf("match history user=%s after deactivation=%+v err=%v", user, matchPage, err)
				}
			}
		})
	}
}
