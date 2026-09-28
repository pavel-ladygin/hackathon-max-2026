package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/discovery"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/generic"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// TestGenericEventFlowsFromNormalizationIntoCatalogAndRoomMatch exercises the
// Stage 9 product path using a deterministic local record and the production
// provider repository, catalog reader and recommendation builder.
func TestGenericEventFlowsFromNormalizationIntoCatalogAndRoomMatch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	f := newRoomFixture(t, db)
	sourceKey := "generic:" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM provider_sync_runs WHERE provider=$1`, sourceKey)
	})
	intentDate := validIntentRequest().Dates[0].Time.UTC()
	start := time.Date(intentDate.Year(), intentDate.Month(), intentDate.Day(), 20, 0, 0, 0, time.UTC)
	externalID := "event-" + uuid.NewString()
	config := generic.Config{
		SourceKey: sourceKey,
		Fields: generic.Fields{
			ExternalID:      generic.FieldMapping{Path: "id", Transform: generic.TransformString},
			Title:           generic.FieldMapping{Path: "event.title", Transform: generic.TransformStripHTML},
			StartsAt:        generic.FieldMapping{Path: "schedule.start", Transform: generic.TransformISODateTime},
			VenueName:       generic.FieldMapping{Path: "place.name", Transform: generic.TransformString},
			VenueAddress:    generic.FieldMapping{Path: "place.address"},
			Latitude:        generic.FieldMapping{Path: "place.lat", Transform: generic.TransformNumber},
			Longitude:       generic.FieldMapping{Path: "place.lng", Transform: generic.TransformNumber},
			TicketURL:       generic.FieldMapping{Path: "links.ticket"},
			TicketAvailable: generic.FieldMapping{Path: "registration.open"},
			Image:           generic.FieldMapping{Path: "images.0.url"},
		},
		Defaults:  generic.Defaults{Category: "concerts", Timezone: "UTC", Currency: "RUB", Status: providers.EventStatusPublished},
		PriceUnit: generic.PriceUnitMajor,
		PriceFrom: generic.FieldMapping{Path: "price.from", Transform: generic.TransformNumber},
	}
	record := map[string]any{
		"id":           externalID,
		"event":        map[string]any{"title": "<b>Generic concert</b>"},
		"schedule":     map[string]any{"start": start.Format(time.RFC3339)},
		"place":        map[string]any{"name": "Generic hall", "address": "1 Main St", "lat": 55.75, "lng": 37.62},
		"links":        map[string]any{"ticket": "https://tickets.example/generic"},
		"registration": map[string]any{"open": true},
		"images":       []any{map[string]any{"url": "https://images.example/generic.jpg"}},
		"price":        map[string]any{"from": 1200.0},
	}
	event, err := generic.Normalize(config, record)
	if err != nil {
		t.Fatalf("normalize generic record: %v", err)
	}
	if event.Title != "Generic concert" || event.StartsAt.IsZero() {
		t.Fatalf("normalized event title/start = %q/%v", event.Title, event.StartsAt)
	}

	repo := providers.NewRepository(db)
	var persistErr error
	ingest, err := providers.NewIngestion(f.city, repo, func(err error) { persistErr = err })
	if err != nil {
		t.Fatal(err)
	}
	ingest.AddFetched(1)
	if err := ingest.Persist(ctx, event); err != nil {
		t.Fatalf("persist normalized record: %v", err)
	}
	unseenRecord := map[string]any{}
	for key, value := range record {
		unseenRecord[key] = value
	}
	unseenRecord["id"] = "unseen-" + uuid.NewString()
	unseenEvent, err := generic.Normalize(config, unseenRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := ingest.Persist(ctx, unseenEvent); err != nil {
		t.Fatalf("persist initially unseen record: %v", err)
	}
	stats := ingest.Stats()
	if stats.Inserted != 2 || stats.Errors != 0 {
		t.Fatalf("initial ingestion stats = %+v; persistence error=%v", stats, persistErr)
	}
	eventID := providerEventID(t, db, sourceKey, externalID)
	unseenID := providerEventID(t, db, sourceKey, unseenEvent.ExternalID)

	// Re-importing the same provider identity updates in place. A later partial
	// run does not reconcile/deactivate records omitted from that run.
	changedRecord := map[string]any{}
	for key, value := range record {
		changedRecord[key] = value
	}
	changedRecord["event"] = map[string]any{"title": "<b>Updated generic concert</b>"}
	changed, err := generic.Normalize(config, changedRecord)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := providers.NewIngestion(f.city, repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	runStarted := time.Now().UTC()
	runID, err := repo.BeginSyncRun(ctx, providers.SyncRunStart{Provider: sourceKey, CityID: f.city, WindowStart: runStarted, WindowEnd: runStarted, UpsertOnly: true})
	if err != nil {
		t.Fatalf("begin partial upsert-only run: %v", err)
	}
	if err := partial.Persist(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if partial.Stats().Updated != 1 {
		t.Fatalf("repeat ingestion stats = %+v; want one update", partial.Stats())
	}
	if err := partial.Persist(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if partial.Stats().Skipped != 1 {
		t.Fatalf("duplicate record stats = %+v; want duplicate skipped", partial.Stats())
	}
	partialStats := partial.Stats()
	partialStats.Errors = 1
	if _, err := repo.FinishSyncRun(ctx, providers.SyncRunFinish{RunID: runID, State: providers.SyncRunFailed, Stats: partialStats}); err != nil {
		t.Fatalf("finish partial upsert-only run: %v", err)
	}
	if got := providerEventID(t, db, sourceKey, externalID); got != eventID {
		t.Fatalf("stable identity changed from %s to %s", eventID, got)
	}
	var active bool
	var title string
	if err := db.QueryRow(ctx, `SELECT provider_active,title FROM events WHERE id=$1`, eventID).Scan(&active, &title); err != nil {
		t.Fatal(err)
	}
	if !active || title != "Updated generic concert" {
		t.Fatalf("upsert state active=%t title=%q", active, title)
	}
	assertProviderActive(t, db, unseenID, true)
	var reconcileMissing bool
	if err := db.QueryRow(ctx, `SELECT reconcile_missing FROM provider_sync_runs WHERE id=$1`, runID).Scan(&reconcileMissing); err != nil {
		t.Fatal(err)
	}
	if reconcileMissing {
		t.Fatal("Generic partial run unexpectedly enabled reconciliation")
	}

	snapshot, err := catalog.NewRepository(db).LoadCity(ctx, f.city)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	var catalogEvent *catalog.Event
	for i := range snapshot.Events {
		if snapshot.Events[i].ID == eventID {
			catalogEvent = &snapshot.Events[i]
			break
		}
	}
	if catalogEvent == nil {
		t.Fatalf("catalog snapshot omitted Generic event %s", eventID)
	}
	if catalogEvent.Title != "Updated generic concert" || len(catalogEvent.Categories) != 1 || len(catalogEvent.Images) != 1 || catalogEvent.Images[0].Role != "card" || !strings.HasPrefix(catalogEvent.Images[0].Url, "/api/v1/event-images/") {
		t.Fatalf("catalog event detail: title=%q categories=%d images=%d", catalogEvent.Title, len(catalogEvent.Categories), len(catalogEvent.Images))
	}
	availability, err := catalog.NewRepository(db).CheckForRoomVote(ctx, eventID)
	if err != nil || !availability.Exists || !availability.TicketAvailable {
		t.Fatalf("catalog availability = %+v, err=%v", availability, err)
	}

	// Exercise the user-facing catalog projections (search/filter, detail and
	// map) over the persisted Generic row instead of only inspecting SQL data.
	discoveryCodec, err := discovery.NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	discoveryService := discovery.NewService(discovery.NewRepository(db), discoveryCodec)
	filterDate := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	searchPage, err := discoveryService.Search(ctx, discovery.SearchFilter{
		UserID: f.creator, CityID: f.city, DateFrom: &filterDate, DateTo: &filterDate,
		CategorySlugs: []string{"concerts"}, PriceMaxMinor: int32Pointer(200000), Limit: 50, IncludeTotal: true,
	})
	if err != nil {
		t.Fatalf("search/filter Generic event: %v", err)
	}
	foundCard := false
	for _, card := range searchPage.Items {
		if card.ID == eventID {
			foundCard = card.Title == "Updated generic concert" && card.CategorySlug == "concerts" && card.ImageURL != nil && strings.HasPrefix(*card.ImageURL, "/api/v1/event-images/")
		}
	}
	if !foundCard {
		t.Fatalf("catalog search omitted or misprojected Generic event; total=%d items=%+v", searchPage.Total, searchPage.Items)
	}
	detail, err := discoveryService.Get(ctx, f.creator, eventID, nil)
	if err != nil || detail.Provenance.Source != sourceKey || detail.Provenance.IsDemo || len(detail.Images) != 1 || detail.Images[0].Role != "card" || !strings.HasPrefix(detail.Images[0].URL, "/api/v1/event-images/") {
		t.Fatalf("Generic detail=%+v err=%v", detail, err)
	}
	mapPage, err := discoveryService.SearchMapPage(ctx, discovery.SearchFilter{
		UserID: f.creator, CityID: f.city, DateFrom: &filterDate, DateTo: &filterDate,
		CategorySlugs: []string{"concerts"}, Bounds: &discovery.Bounds{West: 37.5, South: 55.5, East: 37.8, North: 56}, Limit: 50,
	})
	if err != nil {
		t.Fatalf("search map Generic event: %v", err)
	}
	foundMapPoint := false
	for _, point := range mapPage.Items {
		if point.ID == eventID && point.Latitude != nil && point.Longitude != nil && *point.Latitude == 55.75 && *point.Longitude == 37.62 {
			foundMapPoint = true
		}
	}
	if !foundMapPoint {
		t.Fatalf("map projection omitted Generic event or coordinates: %+v", mapPage.Items)
	}

	// Use the production catalog-backed pool builder to prove this source is
	// eligible for the same recommendation surface as built-in providers.
	day := start.Format(time.DateOnly)
	poolBuilder, err := recommendations.NewPoolBuilderWithClock(catalog.NewRepository(db), []byte("0123456789abcdef0123456789abcdef"), func() time.Time { return time.Now().UTC() })
	if err != nil {
		t.Fatal(err)
	}
	poolInput := contracts.BuildInput{
		RoomID: uuid.New(), CityID: f.city, RoundNo: 1, PoolVersion: 1,
		FirstIntent:  contracts.ParticipantIntent{Dates: []string{day}, TimeSlots: []string{"evening"}, CategorySlugs: []string{"concerts"}, BudgetMaxMinor: 200000},
		SecondIntent: contracts.ParticipantIntent{Dates: []string{day}, TimeSlots: []string{"evening"}, CategorySlugs: []string{"concerts"}, BudgetMaxMinor: 200000},
	}
	build, err := poolBuilder.Build(ctx, poolInput)
	if err != nil {
		t.Fatalf("build recommendation pool: %v", err)
	}
	foundCandidate := false
	candidateIDs := make([]uuid.UUID, 0, len(build.Candidates))
	for _, candidate := range build.Candidates {
		candidateIDs = append(candidateIDs, candidate.EventID)
		if candidate.EventID == eventID {
			foundCandidate = true
		}
	}
	if !foundCandidate {
		t.Fatalf("Generic event %s missing from production pool candidates: %+v", eventID, candidateIDs)
	}

	// Room mechanics accept the production pool output and preserve Generic
	// provenance through both likes and the resulting match.
	f.addTwoMembers(t)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := db.Exec(ctx, `INSERT INTO room_member_round_state(room_id,user_id,round_no) VALUES($1,$2,1)`, f.room, user); err != nil {
			t.Fatal(err)
		}
	}
	roomSvc := newCreateServiceWithBuilder(t, db, behavior.Recorder{}, &poolBuilderFake{result: build})
	codec, err := rooms.NewRoomEventsCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if err := roomSvc.EnableRoomEvents(catalog.NewRepository(db), codec); err != nil {
		t.Fatal(err)
	}
	for _, user := range []uuid.UUID{f.creator, f.member} {
		intent := validIntentRequest()
		intent.Dates[0].Time = intentDate
		intent.BudgetMaxMinor = 200000
		if _, _, err := roomSvc.ReplaceIntent(ctx, contracts.Principal{UserID: user}, f.room, intent); err != nil {
			t.Fatalf("replace room intent for %s: %v", user, err)
		}
	}
	if _, err := roomSvc.Vote(ctx, contracts.Principal{UserID: f.creator}, f.room, eventID, api.VoteRequest{PoolVersion: 1, Vote: api.Like}); err != nil {
		t.Fatalf("first like: %v", err)
	}
	matched, err := roomSvc.Vote(ctx, contracts.Principal{UserID: f.member}, f.room, eventID, api.VoteRequest{PoolVersion: 1, Vote: api.Like})
	if err != nil {
		t.Fatalf("second like: %v", err)
	}
	match, err := matched.Match.Get()
	if err != nil || match.Event.Id != eventID {
		t.Fatalf("match event=%+v err=%v; want Generic event %s", match, err, eventID)
	}
	var matchSource string
	if err := db.QueryRow(ctx, `SELECT e.source FROM room_matches m JOIN events e ON e.id=m.event_id WHERE m.id=$1`, match.Id).Scan(&matchSource); err != nil {
		t.Fatal(err)
	}
	if matchSource != sourceKey {
		t.Fatalf("match source=%q; want %q", matchSource, sourceKey)
	}
}

func providerEventID(t *testing.T, db *store.Pool, source, externalID string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRow(context.Background(), `SELECT id FROM events WHERE source=$1 AND external_id=$2`, source, externalID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func int32Pointer(value int32) *int32 { return &value }
