package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
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
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/kudago"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/tickets"
)

// TestKazanFixturePoC uses only an explicitly isolated database and localhost
// HTTP server. Provider-shaped synthetic cards are never live coverage evidence.
func TestKazanFixturePoC(t *testing.T) {
	dsn := os.Getenv("MULTICITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MULTICITY_TEST_DATABASE_URL is not set")
	}
	if _, err := testdbguard.Parse(dsn, "multicity_fixture"); err != nil {
		t.Fatal("PoC requires the approved isolated multicity fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	defer db.Close()
	moscow := uuid.MustParse(catalogseed.MoscowCityID)
	kazan := uuid.MustParse("3d3c83de-691b-510e-85e5-79685c4012ee")
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng)
		VALUES($1,'Москва','Europe/Moscow',55.7558,37.6173) ON CONFLICT(id) DO NOTHING`, moscow); err != nil {
		t.Fatal(err)
	}
	var existingKazan string
	if err := db.QueryRow(ctx, "SELECT name FROM cities WHERE id=$1", kazan).Scan(&existingKazan); err != nil || existingKazan != "Казань" {
		t.Fatal("migration 000031 must create Kazan before PoC")
	}
	asof := time.Now().UTC()
	zone, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	start := nextSaturday(asof.In(zone)).Add(18 * time.Hour)
	payload := func(location string) map[string]any {
		events := make([]any, 0, 4)
		base, lat, lng := int64(9_100_000), 55.7558, 37.6173
		if location == "kzn" {
			base, lat, lng = 9_200_000, 55.78874, 49.12214
		}
		for i := int64(1); i <= 4; i++ {
			events = append(events, map[string]any{
				"id": base + i, "title": fmt.Sprintf("SYNTHETIC PoC fixture %s %d", location, i),
				"description": "Synthetic local test card. Not a live event or ticket offer.",
				"location":    map[string]any{"slug": location},
				"dates":       []any{map[string]any{"start": start.Unix(), "end": start.Add(time.Hour).Unix()}},
				"categories":  []string{"concert"}, "is_free": true,
				"site_url": fmt.Sprintf("https://kudago.com/%s/event/synthetic-poc-%d/", location, i),
				"images":   []any{map[string]any{"image": "https://kudago.com/media/images/synthetic-poc.jpg"}},
				"place": map[string]any{"id": base + i, "title": "SYNTHETIC fixture hall", "address": "Synthetic local address",
					"coords": map[string]any{"lat": lat, "lon": lng}},
			})
		}
		return map[string]any{"count": len(events), "next": nil, "results": events}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		location := r.URL.Query().Get("location")
		if location != "msk" && location != "kzn" {
			http.Error(w, "unexpected fixture location", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(payload(location))
	}))
	defer server.Close()
	repo := providers.NewRepository(db)
	importCity := func(city uuid.UUID, location string) providers.ImportStats {
		t.Helper()
		client, err := kudago.NewClient(kudago.Options{BaseURL: server.URL, Timeout: time.Second, PageSize: 100,
			Location: location, CityID: city, Timezone: "Europe/Moscow"})
		if err != nil {
			t.Fatal(err)
		}
		stats, err := client.Import(ctx, city, repo, nil)
		if err != nil || stats.Errors != 0 || stats.Inserted+stats.Updated != 4 {
			t.Fatalf("fixture import failed: %v stats=%+v", err, stats)
		}
		return stats
	}
	catalogRepo := catalog.NewRepository(db)
	importCity(moscow, "msk")
	before, err := catalogRepo.LoadCity(ctx, moscow)
	if err != nil {
		t.Fatal(err)
	}
	first := importCity(kazan, "kzn")
	second := importCity(kazan, "kzn")
	if second.Inserted != 0 || second.Updated != 4 {
		t.Fatalf("replay not idempotent: %+v", second)
	}
	after, err := catalogRepo.LoadCity(ctx, moscow)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("Kazan import changed the Moscow snapshot")
	}
	snapshot, err := catalogRepo.LoadCity(ctx, kazan)
	if err != nil || len(snapshot.Events) != 4 {
		t.Fatalf("Kazan catalog count: %d error: %v", len(snapshot.Events), err)
	}
	venues := map[uuid.UUID]bool{}
	for _, venue := range snapshot.Venues {
		if venue.CityID != kazan {
			t.Fatal("foreign venue in Kazan catalog")
		}
		venues[venue.ID] = true
	}
	for _, event := range snapshot.Events {
		if !venues[event.VenueID] || event.Timezone != "Europe/Moscow" || !event.StartsAt.Time.Equal(start) ||
			!event.PriceFromMinor.Valid || event.PriceFromMinor.Int32 != 0 || !event.TicketAvailable ||
			!event.TicketUrl.Valid || len(event.Images) == 0 || len(event.Categories) == 0 || event.Categories[0].CategorySlug != "concerts" {
			t.Fatal("normalized fixture card violates expected catalog fields")
		}
	}
	builder, err := recommendations.NewPoolBuilderWithClock(catalogRepo, []byte("isolated-fixture-only-tie-secret-32"), func() time.Time { return asof })
	if err != nil {
		t.Fatal(err)
	}
	intent := contracts.ParticipantIntent{UserID: uuid.New(), Version: 1, Dates: []string{start.Format(time.DateOnly)},
		DayTypes: []string{"weekend"}, TimeSlots: []string{"evening"}, CategorySlugs: []string{"concerts"}, BudgetMaxMinor: 300000}
	other := intent
	other.UserID = uuid.New()
	result, err := builder.Build(ctx, contracts.BuildInput{RoomID: uuid.New(), CityID: kazan, RoundNo: 1, PoolVersion: 1, FirstIntent: intent, SecondIntent: other})
	if err != nil || len(result.Candidates) != 4 || result.IsSmall {
		t.Fatalf("Kazan pool: candidates=%d small=%v error=%v", len(result.Candidates), result.IsSmall, err)
	}
	knownIDs := map[uuid.UUID]bool{}
	for _, event := range snapshot.Events {
		knownIDs[event.ID] = true
	}
	for _, candidate := range result.Candidates {
		if !knownIDs[candidate.EventID] {
			t.Fatal("pool contains an event outside the verified city/date/budget fixture")
		}
	}
	runKazanFixtureLifecycle(t, ctx, db, builder, kazan, start)
	t.Logf("FIXTURE ONLY as_of=%s city=%s imported=%d replay_updated=%d candidates=%d isSmall=%v hard_constraint_violations=0 Moscow_snapshot_unchanged=true room_lifecycle=passed",
		asof.Format(time.RFC3339), kazan, first.Inserted+first.Updated, second.Updated, len(result.Candidates), result.IsSmall)
}

// runKazanFixtureLifecycle covers the product service boundary with the cards
// imported by this PoC. It intentionally does not delete the append-only
// fixture rows in the isolated database.
func runKazanFixtureLifecycle(t *testing.T, ctx context.Context, db *store.Pool, builder contracts.PoolBuilder, cityID uuid.UUID, start time.Time) {
	t.Helper()
	firstUser, secondUser := uuid.New(), uuid.New()
	for i, userID := range []uuid.UUID{firstUser, secondUser} {
		if _, err := db.Exec(ctx, `INSERT INTO users(id,max_user_id,display_name,city_id,locale,onboarding_state)
			VALUES($1,$2,$3,$4,'ru-RU','complete')`, userID, time.Now().UnixNano()+int64(i), "kazan-poc-"+userID.String(), cityID); err != nil {
			t.Fatalf("insert lifecycle user: %v", err)
		}
	}
	invites, err := rooms.NewInviteCodec([]byte("0123456789abcdef0123456789abcdef"), 1, "https://app.test/invite/{token}", "https://max.test/bot?startapp={token}")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := rooms.NewCreateService(db, behavior.Recorder{}, invites, builder)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := rooms.NewRoomEventsCursorCodec([]byte("abcdef0123456789abcdef0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.EnableRoomEvents(catalog.NewRepository(db), cursor); err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(ctx, contracts.Principal{UserID: firstUser}, "kazan-poc-create-"+uuid.NewString(), api.CreateRoomRequest{CityId: cityID, Name: "Kazan fixture lifecycle"})
	if err != nil || created.Invite.Token == "" {
		t.Fatalf("create fixture room: invite=%+v err=%v", created.Invite, err)
	}
	roomID := created.Room.Id
	joined, err := svc.Join(ctx, contracts.Principal{UserID: secondUser}, created.Invite.Token, "kazan-poc-join-"+uuid.NewString())
	if err != nil || len(joined.Participants) != 2 {
		t.Fatalf("join fixture room: participants=%d err=%v", len(joined.Participants), err)
	}
	zone, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	intent := api.RoomIntentRequest{
		Dates: []openapi_types.Date{{Time: start.In(zone)}}, CategorySlugs: []api.CategorySlug{"concerts"}, BudgetMaxMinor: 300000,
		DayTypes: []api.DayType{}, TimeSlots: []api.TimeSlot{}, ExclusionSlugs: []api.RoomIntentRequestExclusionSlugs{},
		Location: nullable.NewNullNullable[api.GeoPoint](), RadiusM: nullable.NewNullNullable[int](),
	}
	if _, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: firstUser}, roomID, intent); err != nil || transitioned {
		t.Fatalf("first fixture intent transitioned=%t err=%v", transitioned, err)
	}
	if snapshot, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: secondUser}, roomID, intent); err != nil || !transitioned || snapshot.State != api.RoomStateVoting {
		t.Fatalf("second fixture intent state=%s transitioned=%t err=%v", snapshot.State, transitioned, err)
	}
	firstCards, err := svc.GetEvents(ctx, contracts.Principal{UserID: firstUser}, roomID, rooms.RoomEventsInput{Limit: 50})
	if err != nil || len(firstCards.Items) == 0 {
		t.Fatalf("first fixture event cards=%d err=%v", len(firstCards.Items), err)
	}
	secondCards, err := svc.GetEvents(ctx, contracts.Principal{UserID: secondUser}, roomID, rooms.RoomEventsInput{Limit: 50})
	if err != nil || len(secondCards.Items) == 0 || firstCards.Items[0].Event.Id != secondCards.Items[0].Event.Id {
		t.Fatalf("fixture card order first=%v second=%v err=%v", firstCards.Items, secondCards.Items, err)
	}
	eventID := firstCards.Items[0].Event.Id
	vote := api.VoteRequest{PoolVersion: 1, Vote: api.Like}
	if _, err := svc.Vote(ctx, contracts.Principal{UserID: firstUser}, roomID, eventID, vote); err != nil {
		t.Fatalf("first fixture vote: %v", err)
	}
	matched, err := svc.Vote(ctx, contracts.Principal{UserID: secondUser}, roomID, eventID, vote)
	if err != nil {
		t.Fatalf("second fixture vote: %v", err)
	}
	match, err := matched.Match.Get()
	if err != nil || match.Id == uuid.Nil || match.Event.Id != eventID || len(match.Participants) != 2 {
		t.Fatalf("fixture match=%+v err=%v", match, err)
	}
	ticketService, err := tickets.NewService(db, behavior.Recorder{}, []string{"kudago.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ticketService.Click(ctx, secondUser, eventID, roomID); err != nil {
		t.Fatalf("fixture ticket click: %v", err)
	}
	var matches, ticketClicks int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM room_matches WHERE room_id=$1),
		(SELECT count(*) FROM behavior_events WHERE room_id=$1 AND user_id=$2 AND event_id=$3 AND type='ticket_click')`, roomID, secondUser, eventID).Scan(&matches, &ticketClicks); err != nil {
		t.Fatal(err)
	}
	if matches != 1 || ticketClicks != 1 {
		t.Fatalf("fixture lifecycle persisted matches/ticket_clicks=%d/%d, want 1/1", matches, ticketClicks)
	}
}
