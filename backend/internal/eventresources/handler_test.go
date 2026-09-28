package eventresources

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/resourcedomains"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/tickets"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

func TestGenericEventResourcesEnforceRuntimeApprovalAndRevoke(t *testing.T) {
	db := openResourceTestDB(t)
	ctx := context.Background()
	keyBytes := bytes.Repeat([]byte{0x41}, 32)
	codec, err := sourceconfig.NewSecretCodec(keyBytes, 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	domains, err := resourcedomains.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	a := createResourceFixture(t, db, sources, "a")
	b := createResourceFixture(t, db, sources, "b")
	defer cleanupResourceFixture(t, db, a)
	defer cleanupResourceFixture(t, db, b)

	var mu sync.Mutex
	fetchCount := 0
	fetch := func(_ context.Context, raw string) ([]byte, string, error) {
		mu.Lock()
		fetchCount++
		mu.Unlock()
		if raw != "https://cdn.partner.test/a.jpg?token=must-not-leak" {
			t.Errorf("fetch target came from unexpected source: %q", raw)
		}
		return []byte("fixture-image"), "image/jpeg", nil
	}
	router := chi.NewRouter()
	NewHandlerWithFetcher(db, fetch).RegisterRoutes(router)
	imagePath := "/api/v1/event-images/" + a.imageID.String() + "/content"
	ticketPath := "/api/v1/events/" + a.eventID.String() + "/ticket"

	// A client cannot inject or override an upstream URL.
	queryOverride := requestResource(router, http.MethodGet, imagePath+"?url=https://evil.test/x", nil)
	if queryOverride.Code != http.StatusBadRequest {
		t.Fatalf("query override status=%d", queryOverride.Code)
	}
	if got := requestResource(router, http.MethodGet, "/api/v1/event-images/"+uuid.NewString()+"/content", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown image status=%d", got.Code)
	}

	// An approval belongs to source A and to image purpose only.
	approval, err := domains.Add(ctx, a.sourceID, "CDN.PARTNER.TEST", resourcedomains.PurposeImage)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestResource(router, http.MethodGet, imagePath, nil); got.Code != http.StatusOK || got.Body.String() != "fixture-image" || got.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("approved image: status=%d headers=%v body=%q", got.Code, got.Header(), got.Body.String())
	}
	if got := requestResource(router, http.MethodGet, "/api/v1/event-images/"+b.imageID.String()+"/content", nil); got.Code != http.StatusForbidden {
		t.Fatalf("cross-source image status=%d", got.Code)
	}
	if _, err := db.Exec(ctx, `UPDATE events SET ticket_url='https://cdn.partner.test/buy' WHERE id=$1`, a.eventID); err != nil {
		t.Fatal(err)
	}
	if got := requestResource(router, http.MethodGet, ticketPath, nil); got.Code != http.StatusForbidden {
		t.Fatalf("image approval authorized same-host ticket status=%d", got.Code)
	}
	if _, err := db.Exec(ctx, `UPDATE events SET ticket_url='https://tickets.partner.test/buy?secret=never-return-this' WHERE id=$1`, a.eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := domains.Add(ctx, b.sourceID, "cdn.partner.test", resourcedomains.PurposeTicket); err != nil {
		t.Fatal(err)
	}
	if got := requestResource(router, http.MethodGet, "/api/v1/event-images/"+b.imageID.String()+"/content", nil); got.Code != http.StatusForbidden {
		t.Fatalf("ticket-only approval authorized image: %d", got.Code)
	}

	if ok, err := domains.Revoke(ctx, a.sourceID, approval.ID); err != nil || !ok {
		t.Fatalf("revoke=(%t,%v)", ok, err)
	}
	if got := requestResource(router, http.MethodGet, imagePath, nil); got.Code != http.StatusForbidden {
		t.Fatalf("revoked image status=%d", got.Code)
	}
	if _, err := domains.Add(ctx, a.sourceID, "cdn.partner.test", resourcedomains.PurposeImage); err != nil {
		t.Fatal(err)
	}
	if got := requestResource(router, http.MethodGet, imagePath, nil); got.Code != http.StatusOK {
		t.Fatalf("reapproved image status=%d", got.Code)
	}
	if got := requestResource(router, http.MethodGet, ticketPath+"?destination=https://evil.test", nil); got.Code != http.StatusBadRequest {
		t.Fatalf("ticket destination override status=%d", got.Code)
	}
	if got := requestResource(router, http.MethodGet, ticketPath, nil); got.Code != http.StatusForbidden {
		t.Fatalf("unapproved ticket status=%d", got.Code)
	}
	if _, err := domains.Add(ctx, a.sourceID, "tickets.partner.test", resourcedomains.PurposeTicket); err != nil {
		t.Fatal(err)
	}
	ticket := requestResource(router, http.MethodGet, ticketPath, nil)
	if ticket.Code != http.StatusFound || ticket.Header().Get("Location") != "https://tickets.partner.test/buy?secret=never-return-this" || ticket.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("approved ticket status=%d location=%q", ticket.Code, ticket.Header().Get("Location"))
	}
	if got := requestResource(router, http.MethodGet, "/api/v1/events/"+b.eventID.String()+"/ticket", nil); got.Code != http.StatusForbidden {
		t.Fatalf("cross-source ticket status=%d", got.Code)
	}
	for _, raw := range []string{"http://tickets.partner.test/buy", "not a URL", "https://127.0.0.1/buy"} {
		if _, err := db.Exec(ctx, `UPDATE events SET ticket_url=$2 WHERE id=$1`, a.eventID, raw); err != nil {
			t.Fatal(err)
		}
		if got := requestResource(router, http.MethodGet, ticketPath, nil); got.Code != http.StatusForbidden {
			t.Fatalf("unsafe stored ticket %q status=%d", raw, got.Code)
		}
	}
	if _, err := db.Exec(ctx, `UPDATE events SET ticket_url='https://tickets.partner.test/buy?secret=never-return-this' WHERE id=$1`, a.eventID); err != nil {
		t.Fatal(err)
	}
	var ticketApproval uuid.UUID
	if err := db.QueryRow(ctx, `SELECT id FROM event_source_allowed_domains WHERE source_id=$1 AND hostname='tickets.partner.test' AND purpose='ticket'`, a.sourceID).Scan(&ticketApproval); err != nil {
		t.Fatal(err)
	}
	if ok, err := domains.Revoke(ctx, a.sourceID, ticketApproval); err != nil || !ok {
		t.Fatalf("ticket revoke=(%t,%v)", ok, err)
	}
	if got := requestResource(router, http.MethodGet, ticketPath, nil); got.Code != http.StatusForbidden {
		t.Fatalf("revoked ticket status=%d", got.Code)
	}

	if _, err := domains.Add(ctx, a.sourceID, "tickets.partner.test", resourcedomains.PurposeTicket); err != nil {
		t.Fatal(err)
	}
	if got := requestResource(router, http.MethodGet, ticketPath, nil); got.Code != http.StatusFound {
		t.Fatalf("reapproved ticket status=%d", got.Code)
	}

	mu.Lock()
	gotFetches := fetchCount
	mu.Unlock()
	if gotFetches != 2 {
		t.Fatalf("fetch count=%d, want only two approved image requests", gotFetches)
	}
}

func TestImageResourceRejectsUnsafeStoredTargetsAndSanitizesFailure(t *testing.T) {
	db := openResourceTestDB(t)
	codec, err := sourceconfig.NewSecretCodec(bytes.Repeat([]byte{0x42}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	fixture := createResourceFixture(t, db, sources, "unsafe")
	defer cleanupResourceFixture(t, db, fixture)
	domains, _ := resourcedomains.NewRepository(db)
	_, err = domains.Add(context.Background(), fixture.sourceID, "cdn.partner.test", resourcedomains.PurposeImage)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	fetches := 0
	NewHandlerWithFetcher(db, func(context.Context, string) ([]byte, string, error) {
		fetches++
		return nil, "", errors.New("upstream secret internal detail")
	}).RegisterRoutes(router)
	for _, target := range []string{"http://cdn.partner.test/a.jpg", "https://127.0.0.1/a.jpg", "https://localhost/a.jpg", "not a url"} {
		if _, err := db.Exec(context.Background(), `UPDATE event_images SET url=$2 WHERE id=$1`, fixture.imageID, target); err != nil {
			t.Fatal(err)
		}
		response := requestResource(router, http.MethodGet, "/api/v1/event-images/"+fixture.imageID.String()+"/content", nil)
		if response.Code != http.StatusForbidden {
			t.Errorf("target %q status=%d, want 403", target, response.Code)
		}
		if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "upstream") {
			t.Errorf("unsafe target error leaked detail: %s", response.Body.String())
		}
	}
	if fetches != 0 {
		t.Fatalf("unsafe stored target reached fetcher %d times", fetches)
	}
	if _, err := db.Exec(context.Background(), `UPDATE event_images SET url=$2 WHERE id=$1`, fixture.imageID, "https://cdn.partner.test/a.jpg"); err != nil {
		t.Fatal(err)
	}
	response := requestResource(router, http.MethodGet, "/api/v1/event-images/"+fixture.imageID.String()+"/content", nil)
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "upstream secret") {
		t.Fatalf("fetch failure status/body=%d %s", response.Code, response.Body.String())
	}
	if got := requestResource(router, http.MethodGet, "/api/v1/event-images/"+fixture.imageID.String()+"/content?target=https://evil.test", nil); got.Code != http.StatusBadRequest {
		t.Fatalf("target query status=%d", got.Code)
	}
}

func TestImageRevokeDuringFetchBlocksResponseAndReleasesSlot(t *testing.T) {
	db := openResourceTestDB(t)
	ctx := context.Background()
	codec, err := sourceconfig.NewSecretCodec(bytes.Repeat([]byte{0x43}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	fixture := createResourceFixture(t, db, sources, "inflight")
	defer cleanupResourceFixture(t, db, fixture)
	domains, _ := resourcedomains.NewRepository(db)
	approval, err := domains.Add(ctx, fixture.sourceID, "cdn.partner.test", resourcedomains.PurposeImage)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	handler := NewHandlerWithFetcher(db, func(context.Context, string) ([]byte, string, error) {
		close(entered)
		<-release
		return []byte("image"), "image/png", nil
	})
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- requestResource(router, http.MethodGet, "/api/v1/event-images/"+fixture.imageID.String()+"/content", nil)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fetcher was not entered")
	}
	if got := db.Stat().AcquiredConns(); got != 0 {
		t.Errorf("connection held during upstream IO: %d", got)
	}
	if ok, err := domains.Revoke(ctx, fixture.sourceID, approval.ID); err != nil || !ok {
		t.Fatalf("in-flight revoke=(%t,%v)", ok, err)
	}
	close(release)
	if response := <-result; response.Code != http.StatusForbidden {
		t.Fatalf("in-flight revoked response status=%d", response.Code)
	}

	// A failed fetch must release its concurrency slot for the next request.
	if _, err := domains.Add(ctx, fixture.sourceID, "cdn.partner.test", resourcedomains.PurposeImage); err != nil {
		t.Fatal(err)
	}
	handler.fetch = func(context.Context, string) ([]byte, string, error) { return nil, "", errors.New("failed") }
	if response := requestResource(router, http.MethodGet, "/api/v1/event-images/"+fixture.imageID.String()+"/content", nil); response.Code != http.StatusBadGateway {
		t.Fatalf("failed fetch status=%d", response.Code)
	}
	if len(handler.slots) != 0 {
		t.Fatalf("concurrency slot leaked after failure: %d", len(handler.slots))
	}
	for i := 0; i < cap(handler.slots); i++ {
		handler.slots <- struct{}{}
	}
	if response := requestResource(router, http.MethodGet, "/api/v1/event-images/"+fixture.imageID.String()+"/content", nil); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("full proxy concurrency capacity: %d", response.Code)
	}
	for len(handler.slots) > 0 {
		<-handler.slots
	}
}

func openResourceTestDB(t *testing.T) *store.Pool {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("PostgreSQL test database unavailable: %v", err)
	}
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		db.Close()
		t.Fatalf("test database migrations: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

type resourceFixture struct {
	sourceID, eventID, imageID uuid.UUID
	sourceKey                  string
	cityID, venueID            uuid.UUID
}

func createResourceFixture(t *testing.T, db *store.Pool, sources *sourceconfig.Repository, label string) resourceFixture {
	t.Helper()
	ctx := context.Background()
	sourceKey := "generic:resource-test-" + label + "-" + uuid.NewString()
	input := sourceconfig.Input{SourceKey: sourceKey, Name: "Resource test", Enabled: true, EndpointURL: "https://api.partner.test/events", AuthType: "none", Pagination: []byte(`{"mode":"none"}`), Mapping: []byte(`{}`), Defaults: sourceconfig.Defaults{Category: "concerts", Timezone: "UTC", Currency: "RUB", Status: "published"}, PriceUnit: "major"}
	source, err := sources.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	f := resourceFixture{sourceID: source.ID, sourceKey: sourceKey, eventID: uuid.New(), imageID: uuid.New(), cityID: uuid.New(), venueID: uuid.New()}
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'UTC',55.75,37.62)`, f.cityID, "Resource test "+label); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO venues(id,city_id,name,address,latitude,longitude,venue_type) VALUES($1,$2,'Resource venue','Test address',55.75,37.62,'other')`, f.venueID, f.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO events(id,source,external_id,title,description,venue_id,starts_at,timezone,currency,ticket_url,ticket_available,status,provider_active) VALUES($1,$2,$3,'Resource event','', $4, now()+interval '2 days','UTC','RUB','https://tickets.partner.test/buy?secret=never-return-this',true,'published',true)`, f.eventID, sourceKey, label, f.venueID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO event_images(id,event_id,url,role,position) VALUES($1,$2,'https://cdn.partner.test/a.jpg?token=must-not-leak','card',0)`, f.imageID, f.eventID); err != nil {
		t.Fatal(err)
	}
	return f
}

func cleanupResourceFixture(t *testing.T, db *store.Pool, f resourceFixture) {
	t.Helper()
	ctx := context.Background()
	_, _ = db.Exec(ctx, `DELETE FROM events WHERE id=$1`, f.eventID)
	_, _ = db.Exec(ctx, `DELETE FROM venues WHERE id=$1`, f.venueID)
	_, _ = db.Exec(ctx, `DELETE FROM cities WHERE id=$1`, f.cityID)
	_, _ = db.Exec(ctx, `DELETE FROM event_sources WHERE id=$1`, f.sourceID)
}

func requestResource(router http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://app.example.test"+path, bytes.NewReader(body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func TestGenericTicketClickRequiresRuntimePolicyEvenForStaticHost(t *testing.T) {
	db := openResourceTestDB(t)
	ctx := context.Background()
	codec, err := sourceconfig.NewSecretCodec(bytes.Repeat([]byte{0x51}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	a := createResourceFixture(t, db, sources, "click-a")
	defer cleanupResourceFixture(t, db, a)
	b := createResourceFixture(t, db, sources, "click-b")
	defer cleanupResourceFixture(t, db, b)
	user := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO users(id,max_user_id,display_name,city_id,locale,onboarding_state) VALUES($1,$2,'Ticket fixture',$3,'ru-RU','complete')`, user, time.Now().UnixNano(), a.cityID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
	service, err := tickets.NewService(db, behavior.Recorder{}, []string{"tickets.partner.test"})
	if err != nil {
		t.Fatal(err)
	}
	domains, _ := resourcedomains.NewRepository(db)
	assertBlocked := func() {
		t.Helper()
		if _, err := service.Click(ctx, user, a.eventID); !errors.Is(err, tickets.ErrUnavailable) {
			t.Fatalf("Generic click bypassed runtime policy: %v", err)
		}
	}
	assertBlocked()
	if _, err := domains.Add(ctx, a.sourceID, "tickets.partner.test", resourcedomains.PurposeImage); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	if _, err := domains.Add(ctx, b.sourceID, "tickets.partner.test", resourcedomains.PurposeTicket); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	approval, err := domains.Add(ctx, a.sourceID, "tickets.partner.test", resourcedomains.PurposeTicket)
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Click(ctx, user, a.eventID)
	if err != nil || target != "/api/v1/events/"+a.eventID.String()+"/ticket" {
		t.Fatalf("approved click target=%q err=%v", target, err)
	}
	if _, err := domains.Revoke(ctx, a.sourceID, approval.ID); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
}
