package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/discovery"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventsubmissions"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/resourcedomains"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/saved"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/tickets"
)

func TestUserSystemSourceCannotBeScheduledAsAConnector(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	codec, err := sourceconfig.NewSecretCodec([]byte("0123456789abcdef0123456789abcdef"), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Get(ctx, uuid.MustParse("11111111-1111-5111-8111-111111111130")); !errors.Is(err, sourceconfig.ErrNotFound) {
		t.Fatalf("system source Get error = %v, want ErrNotFound", err)
	}
	listed, err := sources.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range listed {
		if source.SourceKey == "user" || source.ID == uuid.MustParse("11111111-1111-5111-8111-111111111130") {
			t.Fatalf("system user source leaked into generic connector list: %+v", source)
		}
	}

	start := time.Now().UTC()
	if _, err := providers.NewRepository(db).BeginSyncRun(ctx, providers.SyncRunStart{
		Provider: "user", CityID: uuid.New(), WindowStart: start, WindowEnd: start,
	}); err == nil {
		t.Fatal("system user source unexpectedly accepted a provider sync run")
	}
}

// TestPublishedUserEventAppearsInCatalogSearchDetailAndSavedEvents guards the
// source=user projection path used after a submission is approved.
func TestPublishedUserEventAppearsInCatalogSearchDetailAndSavedEvents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	f := newRoomFixture(t, db)
	venueID, eventID := uuid.New(), uuid.New()
	start := time.Now().UTC().Add(14 * 24 * time.Hour).Truncate(time.Minute)
	if _, err := db.Exec(ctx, `INSERT INTO venues(id,city_id,name,address,latitude,longitude,venue_type)
		VALUES($1,$2,'User event hall','1 Example Street',55.75,37.61,'other')`, venueID, f.city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO events
		(id,source,external_id,is_demo,title,description,venue_id,starts_at,timezone,price_from_minor,currency,ticket_url,ticket_available,status,published_at,provider_active)
		VALUES($1,'user',$2,false,'Community concert','A user-submitted concert',$3,$4,'UTC',0,'RUB','https://tickets.example.test/event',true,'published',now(),true)`,
		eventID, eventID.String(), venueID, start); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO event_categories(event_id,category_slug,weight,is_primary) VALUES($1,'concerts',1,true)`, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO event_images(id,event_id,url,role,position) VALUES($1,$2,'https://images.example.test/poster.jpg','card',0)`, uuid.New(), eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO saved_events(user_id,event_id) VALUES($1,$2)`, f.creator, eventID); err != nil {
		t.Fatal(err)
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
	if catalogEvent == nil || catalogEvent.Source != "user" {
		t.Fatalf("catalog omitted user event or source: %+v", catalogEvent)
	}
	if len(catalogEvent.Images) != 1 || !strings.HasPrefix(catalogEvent.Images[0].Url, "/api/v1/event-images/") {
		t.Fatalf("user event image was not projected through protected proxy: %+v", catalogEvent.Images)
	}

	codec, err := discovery.NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	discoveryService := discovery.NewService(discovery.NewRepository(db), codec)
	date := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	page, err := discoveryService.Search(ctx, discovery.SearchFilter{
		UserID: f.creator, CityID: f.city, DateFrom: &date, DateTo: &date,
		CategorySlugs: []string{"concerts"}, PriceMaxMinor: int32Pointer(100000), Limit: 20, IncludeTotal: true,
	})
	if err != nil {
		t.Fatalf("search user event: %v", err)
	}
	found := false
	for _, card := range page.Items {
		if card.ID == eventID {
			found = card.Title == "Community concert" && card.ImageURL != nil && strings.HasPrefix(*card.ImageURL, "/api/v1/event-images/")
		}
	}
	if !found {
		t.Fatalf("search omitted or misprojected user event: %+v", page.Items)
	}
	detail, err := discoveryService.Get(ctx, f.creator, eventID, nil)
	if err != nil || detail.Provenance.Source != "user" || detail.Provenance.IsDemo || len(detail.Images) != 1 || !strings.HasPrefix(detail.Images[0].URL, "/api/v1/event-images/") {
		t.Fatalf("user event detail=%+v err=%v", detail, err)
	}

	savedService, err := saved.NewService(db, behavior.Recorder{}, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	savedPage, err := savedService.List(ctx, f.creator, saved.ListInput{Tab: saved.TabSaved, Limit: 20})
	if err != nil {
		t.Fatalf("list saved user event: %v", err)
	}
	if len(savedPage.Items) != 1 || savedPage.Items[0].Event.ID != eventID || !savedPage.Items[0].Event.Saved || savedPage.Items[0].Event.ImageURL == nil || !strings.HasPrefix(*savedPage.Items[0].Event.ImageURL, "/api/v1/event-images/") {
		t.Fatalf("saved events omitted or misprojected user event: %+v", savedPage.Items)
	}
}

func TestUserEventsRespectRoomTicketEligibilityAndTicketDomainRevocation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	f := newRoomFixture(t, db)
	allowedURL := "https://tickets.user-events.test/show"
	blockedURL := "https://tickets.user-events.test/blocked"
	venueID := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO venues(id,city_id,name,address,latitude,longitude,venue_type)
		VALUES($1,$2,'User event room hall','1 Example Street',NULL,NULL,'other')`, venueID, f.city); err != nil {
		t.Fatal(err)
	}
	startsAt := time.Now().UTC().AddDate(0, 0, 5)
	startsAt = time.Date(startsAt.Year(), startsAt.Month(), startsAt.Day(), 20, 0, 0, 0, time.UTC)
	allowedID, blockedID := uuid.New(), uuid.New()
	for _, item := range []struct {
		id        uuid.UUID
		title     string
		ticketURL *string
		available bool
	}{{allowedID, "Approved user event", &allowedURL, true}, {blockedID, "User event without tickets", &blockedURL, false}} {
		if _, err := db.Exec(ctx, `INSERT INTO events(id,source,external_id,is_demo,title,description,venue_id,starts_at,timezone,price_from_minor,currency,ticket_url,ticket_available,status,published_at,provider_active)
			VALUES($1,'user',$2,false,$3,'Public event',$4,$5,'UTC',0,'RUB',$6,$7,'published',now(),true)`, item.id, item.id.String(), item.title, venueID, startsAt, item.ticketURL, item.available); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO event_categories(event_id,category_slug,weight,is_primary) VALUES($1,'concerts',1,true)`, item.id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO event_images(id,event_id,url,role,position) VALUES($1,$2,$3,'card',0)`, uuid.New(), item.id, "https://images.user-events.test/"+item.id.String()+".jpg"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM behavior_events WHERE event_id=ANY($1::uuid[])`, []uuid.UUID{allowedID, blockedID})
		_, _ = db.Exec(context.Background(), `DELETE FROM events WHERE id=ANY($1::uuid[])`, []uuid.UUID{allowedID, blockedID})
		_, _ = db.Exec(context.Background(), `DELETE FROM venues WHERE id=$1`, venueID)
	})

	domains, err := resourcedomains.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := domains.Add(ctx, uuid.MustParse(eventsubmissions.UserSourceID), "tickets.user-events.test", resourcedomains.PurposeTicket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM event_source_allowed_domains WHERE id=$1`, domain.ID)
	})

	day := startsAt.Format(time.DateOnly)
	poolBuilder, err := recommendations.NewPoolBuilderWithClock(catalog.NewRepository(db), []byte("0123456789abcdef0123456789abcdef"), func() time.Time { return time.Now().UTC() })
	if err != nil {
		t.Fatal(err)
	}
	intent := contracts.ParticipantIntent{Dates: []string{day}, TimeSlots: []string{"evening"}, CategorySlugs: []string{"concerts"}, BudgetMaxMinor: 100000}
	pool, err := poolBuilder.Build(ctx, contracts.BuildInput{RoomID: uuid.New(), CityID: f.city, RoundNo: 1, PoolVersion: 1, FirstIntent: intent, SecondIntent: intent})
	if err != nil {
		t.Fatalf("build room pool for user events: %v", err)
	}
	var foundApproved, foundNoTicket bool
	for _, candidate := range pool.Candidates {
		foundApproved = foundApproved || candidate.EventID == allowedID
		foundNoTicket = foundNoTicket || candidate.EventID == blockedID
	}
	if !foundApproved || foundNoTicket {
		t.Fatalf("user event eligibility: approved-with-link=%t no-ticket=%t candidates=%+v", foundApproved, foundNoTicket, pool.Candidates)
	}

	ticketService, err := tickets.NewService(db, behavior.Recorder{}, []string{"unused.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := ticketService.Click(ctx, f.creator, allowedID)
	wantRedirect := "/api/v1/events/" + allowedID.String() + "/ticket"
	if err != nil || redirect != wantRedirect {
		t.Fatalf("user ticket redirect=%q err=%v; want checked redirect %q", redirect, err, wantRedirect)
	}
	if revoked, err := domains.Revoke(ctx, uuid.MustParse(eventsubmissions.UserSourceID), domain.ID); err != nil || !revoked {
		t.Fatalf("revoke ticket domain = %t, %v", revoked, err)
	}
	if _, err := ticketService.Click(ctx, f.creator, allowedID); !errors.Is(err, tickets.ErrUnavailable) {
		t.Fatalf("ticket click after domain revocation err=%v; want unavailable", err)
	}
}
