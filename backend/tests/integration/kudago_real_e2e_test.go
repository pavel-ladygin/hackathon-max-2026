package integration

import (
	"context"
	"errors"
	"os"
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
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/tickets"
)

func TestKudaGoRealDataEndToEnd(t *testing.T) {
	if os.Getenv("RUN_KUDAGO_REAL_E2E") != "1" {
		t.Skip("RUN_KUDAGO_REAL_E2E is not set")
	}
	db := openTestDB(t)
	ctx := context.Background()
	cityID := uuid.MustParse(catalogseed.MoscowCityID)

	var imported, distinct, missingPrice, unavailableTickets, categoryRows, imageRows int
	if err := db.QueryRow(ctx, `SELECT
		count(*), count(DISTINCT external_id),
		count(*) FILTER (WHERE price_from_minor IS NULL),
		count(*) FILTER (WHERE NOT ticket_available OR ticket_url IS NULL),
		(SELECT count(*) FROM event_categories ec JOIN events ce ON ce.id=ec.event_id WHERE ce.source='kudago'),
		(SELECT count(*) FROM event_images ei JOIN events ie ON ie.id=ei.event_id WHERE ie.source='kudago')
		FROM events WHERE source='kudago'`).Scan(&imported, &distinct, &missingPrice, &unavailableTickets, &categoryRows, &imageRows); err != nil {
		t.Fatal(err)
	}
	if imported == 0 || imported != distinct || missingPrice == 0 || categoryRows == 0 || imageRows == 0 {
		t.Fatalf("catalog invariants imported=%d distinct=%d missing_price=%d unavailable_ticket=%d categories=%d images=%d", imported, distinct, missingPrice, unavailableTickets, categoryRows, imageRows)
	}

	var probeID uuid.UUID
	var startsAt time.Time
	var category string
	var lat, lng float64
	if err := db.QueryRow(ctx, `SELECT e.id,e.starts_at,ec.category_slug,v.latitude,v.longitude
		FROM events e JOIN venues v ON v.id=e.venue_id
		JOIN event_categories ec ON ec.event_id=e.id AND ec.is_primary
		WHERE e.source='kudago' AND e.status='published' AND e.ticket_available
		AND e.ticket_url IS NOT NULL AND e.price_from_minor=0 AND e.starts_at>now()
		ORDER BY e.starts_at,e.id LIMIT 1`).Scan(&probeID, &startsAt, &category, &lat, &lng); err != nil {
		t.Fatalf("select eligible real event: %v", err)
	}

	builder, err := recommendations.NewPoolBuilder(catalog.NewRepository(db), []byte("kudago-g6-real-data-tie-break-key"))
	if err != nil {
		t.Fatal(err)
	}
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	radius := int32(1500)
	participant := func(user uuid.UUID) contracts.ParticipantIntent {
		return contracts.ParticipantIntent{
			UserID: user, Version: 1, Dates: []string{startsAt.In(moscow).Format(time.DateOnly)},
			CategorySlugs: []string{category}, BudgetMaxMinor: 1_000_000,
			RadiusM: &radius, Location: &contracts.GeoPoint{Latitude: lat, Longitude: lng},
		}
	}
	firstUser, secondUser := uuid.New(), uuid.New()
	buildInput := contracts.BuildInput{
		RoomID: uuid.New(), CityID: cityID, RoundNo: 1, PoolVersion: 1,
		FirstIntent: participant(firstUser), SecondIntent: participant(secondUser),
	}
	result, err := builder.Build(ctx, buildInput)
	if err != nil || len(result.Candidates) == 0 {
		t.Fatalf("production PoolBuilder candidates=%d error=%v", len(result.Candidates), err)
	}
	var realCandidate uuid.UUID
	var realCandidateSnapshot contracts.Candidate
	for _, candidate := range result.Candidates {
		var source string
		if err := db.QueryRow(ctx, "SELECT source FROM events WHERE id=$1", candidate.EventID).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if source == "kudago" {
			realCandidate = candidate.EventID
			realCandidateSnapshot = candidate
			if candidate.Score.GroupScore <= 0 || len(candidate.Explanation) == 0 {
				t.Fatalf("real candidate has no score/explanation: %+v", candidate)
			}
			break
		}
	}
	if realCandidate == uuid.Nil {
		t.Fatalf("probe %s produced no KudaGo candidate", probeID)
	}
	containsCandidate := func(result contracts.BuildResult, eventID uuid.UUID) bool {
		for _, candidate := range result.Candidates {
			if candidate.EventID == eventID {
				return true
			}
		}
		return false
	}
	var originalPriceFrom, originalPriceTo *int32
	var originalTicketURL *string
	var originalTicketAvailable bool
	var originalStatus string
	if err := db.QueryRow(ctx, `SELECT price_from_minor,price_to_minor,ticket_url,ticket_available,status FROM events WHERE id=$1`, realCandidate).
		Scan(&originalPriceFrom, &originalPriceTo, &originalTicketURL, &originalTicketAvailable, &originalStatus); err != nil {
		t.Fatal(err)
	}
	restoreCandidate := func() {
		_, _ = db.Exec(context.Background(), `UPDATE events SET price_from_minor=$2,price_to_minor=$3,ticket_url=$4,ticket_available=$5,status=$6 WHERE id=$1`,
			realCandidate, originalPriceFrom, originalPriceTo, originalTicketURL, originalTicketAvailable, originalStatus)
	}
	t.Cleanup(restoreCandidate)
	assertFiltered := func(name, update string) {
		t.Helper()
		if _, err := db.Exec(ctx, update, realCandidate); err != nil {
			t.Fatal(err)
		}
		filtered, err := builder.Build(ctx, buildInput)
		if err != nil {
			t.Fatal(err)
		}
		if containsCandidate(filtered, realCandidate) {
			t.Fatalf("%s event remained in PoolBuilder candidates", name)
		}
		restoreCandidate()
	}
	assertFiltered("missing price", `UPDATE events SET price_from_minor=NULL,price_to_minor=NULL WHERE id=$1`)
	assertFiltered("unavailable ticket", `UPDATE events SET ticket_url=NULL,ticket_available=false WHERE id=$1`)
	assertFiltered("cancelled", `UPDATE events SET status='cancelled' WHERE id=$1`)
	assertFiltered("sold out", `UPDATE events SET status='sold_out' WHERE id=$1`)
	outside := buildInput
	outside.RoomID = uuid.New()
	outside.FirstIntent = participant(firstUser)
	outside.SecondIntent = participant(secondUser)
	outside.FirstIntent.Location = &contracts.GeoPoint{Latitude: 0, Longitude: 0}
	outside.SecondIntent.Location = &contracts.GeoPoint{Latitude: 0, Longitude: 0}
	outsideResult, err := builder.Build(ctx, outside)
	if err != nil {
		t.Fatal(err)
	}
	if containsCandidate(outsideResult, realCandidate) {
		t.Fatal("outside-radius real event remained in PoolBuilder candidates")
	}

	// Add a deterministic demo twin to the persisted pool so source parity does
	// not depend on whichever demo seed happens to match today's live KudaGo
	// date/category/location window.
	demoEvent := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO events
		(id,source,external_id,is_demo,title,subtitle,description,venue_id,starts_at,ends_at,timezone,
		 price_from_minor,price_to_minor,currency,ticket_url,ticket_available,status,age_rating,indoor,loudness_level,published_at)
		SELECT $1,'demo',$2,true,title,subtitle,description,venue_id,starts_at,ends_at,timezone,
		       price_from_minor,price_to_minor,currency,'https://tickets.example/s6-demo',true,'published',age_rating,indoor,loudness_level,published_at
		FROM events WHERE id=$3`, demoEvent, "s6-parity-"+demoEvent.String(), realCandidate); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO event_categories(event_id,category_slug,weight,is_primary)
		SELECT $1,category_slug,weight,is_primary FROM event_categories WHERE event_id=$2`, demoEvent, realCandidate); err != nil {
		t.Fatal(err)
	}
	demoCandidateSnapshot := realCandidateSnapshot
	demoCandidateSnapshot.EventID = demoEvent

	for _, user := range []uuid.UUID{firstUser, secondUser} {
		if _, err := db.Exec(ctx, "INSERT INTO users(id,max_user_id,display_name,city_id) VALUES($1,$2,$3,$4)", user, atomic.AddInt64(&testMaxUserID, 1), "g6-real-"+user.String(), cityID); err != nil {
			t.Fatal(err)
		}
	}
	var roomID uuid.UUID
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if roomID != uuid.Nil {
			_, _ = db.Exec(cleanupCtx, "DELETE FROM behavior_events WHERE room_id=$1", roomID)
			_, _ = db.Exec(cleanupCtx, "DELETE FROM room_matches WHERE room_id=$1", roomID)
			_, _ = db.Exec(cleanupCtx, "DELETE FROM rooms WHERE id=$1", roomID)
		}
		_, _ = db.Exec(cleanupCtx, "DELETE FROM behavior_events WHERE user_id=ANY($1)", []uuid.UUID{firstUser, secondUser})
		_, _ = db.Exec(cleanupCtx, "DELETE FROM users WHERE id=ANY($1)", []uuid.UUID{firstUser, secondUser})
		_, _ = db.Exec(cleanupCtx, "DELETE FROM events WHERE id=$1", demoEvent)
	})

	svc := newCreateServiceWithBuilder(t, db, behavior.Recorder{}, poolBuilderWithCandidate{base: builder, candidate: demoCandidateSnapshot})
	codec, err := rooms.NewRoomEventsCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.EnableRoomEvents(catalog.NewRepository(db), codec); err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(ctx, contracts.Principal{UserID: firstUser}, "kudago-g6-create", api.CreateRoomRequest{CityId: cityID, Name: "KudaGo G6"})
	if err != nil {
		t.Fatal(err)
	}
	roomID = created.Room.Id
	if _, err := svc.Join(ctx, contracts.Principal{UserID: secondUser}, created.Invite.Token, "kudago-g6-join"); err != nil {
		t.Fatal(err)
	}
	request := api.RoomIntentRequest{
		Dates: []openapi_types.Date{{Time: startsAt.In(moscow)}}, CategorySlugs: []api.CategorySlug{api.CategorySlug(category)},
		BudgetMaxMinor: 1_000_000, RadiusM: nullable.NewNullableWithValue(int(radius)),
		Location: nullable.NewNullableWithValue(api.GeoPoint{Lat: float32(lat), Lng: float32(lng)}),
		DayTypes: []api.DayType{}, TimeSlots: []api.TimeSlot{}, ExclusionSlugs: []api.RoomIntentRequestExclusionSlugs{},
	}
	if _, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: firstUser}, roomID, request); err != nil || transitioned {
		t.Fatalf("first intent transitioned=%v error=%v", transitioned, err)
	}
	if _, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: secondUser}, roomID, request); err != nil || !transitioned {
		t.Fatalf("second intent transitioned=%v error=%v", transitioned, err)
	}

	var matchedEvent uuid.UUID
	if err := db.QueryRow(ctx, `SELECT rpe.event_id FROM room_pool_events rpe
		JOIN room_pools rp ON rp.id=rpe.pool_id JOIN events e ON e.id=rpe.event_id
		WHERE rp.room_id=$1 AND e.source='kudago' ORDER BY rpe.position LIMIT 1`, roomID).Scan(&matchedEvent); err != nil {
		t.Fatalf("read immutable real-event pool: %v", err)
	}
	var invalidPoolRows int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM room_pool_events rpe JOIN room_pools rp ON rp.id=rpe.pool_id
		JOIN events e ON e.id=rpe.event_id WHERE rp.room_id=$1
		AND (e.status<>'published' OR NOT e.ticket_available OR e.ticket_url IS NULL OR e.price_from_minor IS NULL)`, roomID).Scan(&invalidPoolRows); err != nil || invalidPoolRows != 0 {
		t.Fatalf("invalid hard-filter rows=%d error=%v", invalidPoolRows, err)
	}

	// Availability is live even though ranking is an immutable snapshot. Verify
	// the same post-build behavior for an imported KudaGo card and a demo card.
	targets := map[string]uuid.UUID{"kudago": matchedEvent, "demo": demoEvent}

	for source, target := range targets {
		var originalPriceFrom, originalPriceTo *int32
		var originalTicketURL *string
		var originalTicketAvailable bool
		var originalStatus string
		if err := db.QueryRow(ctx, `SELECT price_from_minor,price_to_minor,ticket_url,ticket_available,status FROM events WHERE id=$1`, target).
			Scan(&originalPriceFrom, &originalPriceTo, &originalTicketURL, &originalTicketAvailable, &originalStatus); err != nil {
			t.Fatal(err)
		}
		restore := func() {
			if _, err := db.Exec(ctx, `UPDATE events SET price_from_minor=$2,price_to_minor=$3,ticket_url=$4,ticket_available=$5,status=$6 WHERE id=$1`,
				target, originalPriceFrom, originalPriceTo, originalTicketURL, originalTicketAvailable, originalStatus); err != nil {
				t.Fatal(err)
			}
		}
		var snapshot string
		if err := db.QueryRow(ctx, `SELECT row_to_json(rpe)::text FROM room_pool_events rpe
			JOIN room_pools rp ON rp.id=rpe.pool_id WHERE rp.room_id=$1 AND rpe.event_id=$2`, roomID, target).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		mutations := []struct {
			name string
			sql  string
		}{
			{name: "cancelled", sql: `UPDATE events SET status='cancelled' WHERE id=$1`},
			{name: "sold out", sql: `UPDATE events SET status='sold_out' WHERE id=$1`},
			{name: "ticket unavailable", sql: `UPDATE events SET ticket_available=false WHERE id=$1`},
			{name: "new price above budget", sql: `UPDATE events SET price_from_minor=1000001 WHERE id=$1`},
		}
		for _, mutation := range mutations {
			if _, err := db.Exec(ctx, mutation.sql, target); err != nil {
				t.Fatal(err)
			}
			page, err := svc.GetEvents(ctx, contracts.Principal{UserID: firstUser}, roomID, rooms.RoomEventsInput{Limit: 50})
			if err != nil {
				t.Fatalf("%s %s GetEvents: %v", source, mutation.name, err)
			}
			for _, item := range page.Items {
				if item.Event.Id == target {
					t.Fatalf("%s %s event remained visible after pool creation", source, mutation.name)
				}
			}
			if _, err := svc.Vote(ctx, contracts.Principal{UserID: firstUser}, roomID, target, api.VoteRequest{PoolVersion: 1, Vote: api.Like}); !errors.Is(err, rooms.ErrEventUnavailable) {
				t.Fatalf("%s %s vote error=%v, want EVENT_UNAVAILABLE", source, mutation.name, err)
			}
			var votes, matches int
			if err := db.QueryRow(ctx, `SELECT count(*) FROM room_votes WHERE room_id=$1 AND event_id=$2`, roomID, target).Scan(&votes); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, `SELECT count(*) FROM room_matches WHERE room_id=$1`, roomID).Scan(&matches); err != nil {
				t.Fatal(err)
			}
			if votes != 0 || matches != 0 {
				t.Fatalf("%s %s persisted votes=%d matches=%d", source, mutation.name, votes, matches)
			}
			var afterMutation string
			if err := db.QueryRow(ctx, `SELECT row_to_json(rpe)::text FROM room_pool_events rpe
				JOIN room_pools rp ON rp.id=rpe.pool_id WHERE rp.room_id=$1 AND rpe.event_id=$2`, roomID, target).Scan(&afterMutation); err != nil {
				t.Fatal(err)
			}
			if afterMutation != snapshot {
				t.Fatalf("%s %s changed immutable pool snapshot", source, mutation.name)
			}
			restore()
		}
	}

	vote := api.VoteRequest{PoolVersion: 1, Vote: api.Like}
	if _, err := svc.Vote(ctx, contracts.Principal{UserID: firstUser}, roomID, matchedEvent, vote); err != nil {
		t.Fatal(err)
	}
	matched, err := svc.Vote(ctx, contracts.Principal{UserID: secondUser}, roomID, matchedEvent, vote)
	if err != nil {
		t.Fatal(err)
	}
	match, err := matched.Match.Get()
	if err != nil || match.Event.Id != matchedEvent {
		t.Fatalf("match=%+v error=%v", match, err)
	}
	ticketService, err := tickets.NewService(db, behavior.Recorder{}, []string{"kudago.com", "*.kudago.com"})
	if err != nil {
		t.Fatal(err)
	}
	url, err := ticketService.Click(ctx, firstUser, matchedEvent)
	if err != nil || url == "" {
		t.Fatalf("KudaGo external action URL=%q error=%v", url, err)
	}
}

type poolBuilderWithCandidate struct {
	base      contracts.PoolBuilder
	candidate contracts.Candidate
}

func (b poolBuilderWithCandidate) Build(ctx context.Context, input contracts.BuildInput) (contracts.BuildResult, error) {
	result, err := b.base.Build(ctx, input)
	if err != nil {
		return contracts.BuildResult{}, err
	}
	for _, candidate := range result.Candidates {
		if candidate.EventID == b.candidate.EventID {
			return result, nil
		}
	}
	result.Candidates = append(result.Candidates, b.candidate)
	result.IsSmall = len(result.Candidates) <= 2
	return result, nil
}
