package eventsources

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/discovery"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventresources"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providerpolicy"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/generic"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/tickets"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

func TestHandlerPreviewAndManualSyncAgainstPostgres(t *testing.T) {
	db := openEventSourcesTestDB(t)
	moscowID := uuid.MustParse(catalogseed.MoscowCityID)
	if _, err := db.Exec(context.Background(), `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,'Москва','Europe/Moscow',55.75,37.61) ON CONFLICT(id) DO NOTHING`, moscowID); err != nil {
		t.Fatalf("ensure runner city fixture: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	codec, err := sourceconfig.NewSecretCodec(bytes.Repeat([]byte{0x42}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(ctx, db, sources)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)

	sourceKey := "generic:handler-test-" + uuid.NewString()
	externalID := "external-" + uuid.NewString()
	secret := "handler-test-secret-never-return-this"
	input := handlerTestInput(sourceKey, secret, externalID)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = db.Exec(cleanupCtx, `DELETE FROM provider_sync_runs WHERE provider=$1`, sourceKey)
		_, _ = db.Exec(cleanupCtx, `WITH owned AS (SELECT DISTINCT venue_id FROM events WHERE source=$1), deleted AS (DELETE FROM events WHERE source=$1) DELETE FROM venues WHERE id IN (SELECT venue_id FROM owned) AND NOT EXISTS (SELECT 1 FROM events WHERE venue_id IN (SELECT venue_id FROM owned))`, sourceKey)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM event_sources WHERE source_key=$1`, sourceKey)
	})

	var fetchedPreviewSecret string
	handler.previewFn = func(ctx context.Context, got sourceconfig.Input, gotSecret string) (PreviewResult, error) {
		fetchedPreviewSecret = gotSecret
		return previewWithFetcher(ctx, got, gotSecret, func(context.Context, generic.HTTPConfig, generic.PaginationConfig) ([]any, bool, error) {
			return []any{handlerTestRecord(externalID)}, true, nil
		})
	}

	createBody, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	created := doHandlerRequest(t, router, http.MethodPost, "/api/v1/internal/event-sources/", createBody, true)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), secret) || !strings.Contains(created.Body.String(), `"secret_configured":true`) {
		t.Fatalf("create response disclosed secret or omitted configured flag: %s", created.Body.String())
	}
	var createdEnvelope struct {
		Source sourceconfig.Source `json:"source"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdEnvelope); err != nil {
		t.Fatal(err)
	}
	if createdEnvelope.Source.ID == uuid.Nil || createdEnvelope.Source.SourceKey != sourceKey {
		t.Fatalf("created source = %+v", createdEnvelope.Source)
	}
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE source=$1`, sourceKey).Scan(new(int)); err != nil {
		t.Fatal(err)
	}

	// Preview submits source_id with an empty write-only secret, forcing the
	// handler to load the stored credential and validating without persistence.
	previewInput := input
	previewInput.Secret = ""
	previewBody, err := json.Marshal(struct {
		SourceID uuid.UUID `json:"source_id"`
		sourceconfig.Input
	}{createdEnvelope.Source.ID, previewInput})
	if err != nil {
		t.Fatal(err)
	}
	previewResponse := doHandlerRequest(t, router, http.MethodPost, "/api/v1/internal/event-sources/test", previewBody, true)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewResponse.Code, previewResponse.Body.String())
	}
	if fetchedPreviewSecret != secret {
		t.Fatalf("preview resolved stored secret %q, want saved secret", fetchedPreviewSecret)
	}
	var preview PreviewResult
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.ConnectionOK || preview.Received != 1 || preview.Valid != 1 || preview.Invalid != 0 || len(preview.Preview) != 1 {
		t.Fatalf("preview result = %+v", preview)
	}
	var previewWrites int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE source=$1`, sourceKey).Scan(&previewWrites); err != nil {
		t.Fatal(err)
	}
	if previewWrites != 0 {
		t.Fatalf("preview persisted %d events", previewWrites)
	}

	// Blank secret on update must preserve the encrypted credential. The sync
	// runner invokes the ordinary import pipeline with only the JSON fetcher
	// replaced; persistence and sync-run auditing still use PostgreSQL.
	updatedInput := input
	updatedInput.Secret = ""
	updatedInput.Name = "Updated generic fixture"
	updateBody, err := json.Marshal(updatedInput)
	if err != nil {
		t.Fatal(err)
	}
	updated := doHandlerRequest(t, router, http.MethodPatch, "/api/v1/internal/event-sources/"+createdEnvelope.Source.ID.String(), updateBody, true)
	if updated.Code != http.StatusOK || strings.Contains(updated.Body.String(), secret) || !strings.Contains(updated.Body.String(), `"secret_configured":true`) {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}

	var runnerSecret string
	runDone := make(chan error, 1)
	runStarted := make(chan struct{})
	releaseRun := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRun) }) }
	t.Cleanup(release)
	handler.runner.importFn = func(ctx context.Context, cityID uuid.UUID, source sourceconfig.Source, gotSecret string, syncStore providers.SyncStore) (providers.ImportStats, error) {
		runnerSecret = gotSecret
		close(runStarted)
		<-releaseRun
		stats, err := importRecordsWithFetcher(ctx, cityID, source, gotSecret, syncStore, func(context.Context, generic.HTTPConfig, generic.PaginationConfig) ([]any, int, error) {
			if gotSecret != secret {
				return nil, 0, context.DeadlineExceeded
			}
			return []any{handlerTestRecord(externalID)}, 1, nil
		})
		runDone <- err
		return stats, err
	}
	syncResponse := doHandlerRequest(t, router, http.MethodPost, "/api/v1/internal/event-sources/"+createdEnvelope.Source.ID.String()+"/sync", nil, true)
	if syncResponse.Code != http.StatusAccepted || !strings.Contains(syncResponse.Body.String(), `"status":"started"`) {
		t.Fatalf("sync status=%d body=%s", syncResponse.Code, syncResponse.Body.String())
	}
	select {
	case <-runStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("manual sync runner did not start")
	}
	duplicateSync := doHandlerRequest(t, router, http.MethodPost, "/api/v1/internal/event-sources/"+createdEnvelope.Source.ID.String()+"/sync", nil, true)
	if duplicateSync.Code != http.StatusConflict {
		t.Fatalf("parallel sync status=%d body=%s; want 409", duplicateSync.Code, duplicateSync.Body.String())
	}
	release()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("import runner: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manual sync did not finish within 5 seconds")
	}
	if runnerSecret != secret {
		t.Fatalf("sync resolved secret %q, want saved secret", runnerSecret)
	}
	var imported, active int
	if err := db.QueryRow(context.Background(), `SELECT count(*),count(*) FILTER (WHERE provider_active) FROM events WHERE source=$1 AND external_id=$2`, sourceKey, externalID).Scan(&imported, &active); err != nil {
		t.Fatal(err)
	}
	if imported != 1 || active != 1 {
		t.Fatalf("sync persisted imported=%d active=%d; want 1,1", imported, active)
	}
	var runState string
	if err := db.QueryRow(context.Background(), `SELECT state FROM provider_sync_runs WHERE provider=$1 ORDER BY started_at DESC LIMIT 1`, sourceKey).Scan(&runState); err != nil {
		t.Fatal(err)
	}
	if runState != string(providers.SyncRunSucceeded) {
		t.Fatalf("sync run state=%q", runState)
	}
}

func TestGenericEventSourcesBrowserAgainstLiveHandler(t *testing.T) {
	if os.Getenv("RUN_GENERIC_BROWSER_E2E") != "1" {
		t.Skip("set RUN_GENERIC_BROWSER_E2E=1 to run the live-browser event-source workflow")
	}
	db := openEventSourcesTestDB(t)
	moscowID := uuid.MustParse(catalogseed.MoscowCityID)
	if _, err := db.Exec(context.Background(), `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,'Москва','Europe/Moscow',55.75,37.61) ON CONFLICT(id) DO NOTHING`, moscowID); err != nil {
		t.Fatalf("ensure runner city fixture: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	codec, err := sourceconfig.NewSecretCodec(bytes.Repeat([]byte{0x43}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(ctx, db, sources)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	sourceKey := "generic:browser-source-" + suffix
	externalID := "browser-event-" + suffix
	userID := uuid.New()
	if _, err := db.Exec(context.Background(), `INSERT INTO users(id,max_user_id,display_name,city_id,locale,onboarding_state) VALUES($1,$2,'Browser fixture user',$3,'ru-RU','complete')`, userID, time.Now().UnixNano(), moscowID); err != nil {
		t.Fatalf("insert browser fixture user: %v", err)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO user_preferences(user_id,budget_max_minor,usual_day_types,usual_time_slots) VALUES($1,100000,'{}','{}')`, userID); err != nil {
		t.Fatalf("insert browser fixture preferences: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = db.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM provider_sync_runs WHERE provider=$1`, sourceKey)
		_, _ = db.Exec(cleanupCtx, `WITH owned AS (SELECT DISTINCT venue_id FROM events WHERE source=$1), deleted AS (DELETE FROM events WHERE source=$1) DELETE FROM venues WHERE id IN (SELECT venue_id FROM owned) AND NOT EXISTS (SELECT 1 FROM events WHERE venue_id IN (SELECT venue_id FROM owned))`, sourceKey)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM event_sources WHERE source_key=$1`, sourceKey)
	})

	fixture := func() []any {
		return []any{map[string]any{
			"id": externalID, "title": "Live handler event",
			"starts_at":  time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339),
			"venue":      map[string]any{"name": "Live handler venue", "address": "1 Browser Test Way", "latitude": 55.75, "longitude": 37.62},
			"image_url":  "https://images.example.test/poster.png?access_token=fixture-secret",
			"ticket_url": "https://tickets.example.test/live-browser?key=fixture-secret", "ticket_available": true, "price": 10.0,
		}}
	}
	handler.previewFn = func(ctx context.Context, input sourceconfig.Input, secret string) (PreviewResult, error) {
		return previewWithFetcher(ctx, input, secret, func(context.Context, generic.HTTPConfig, generic.PaginationConfig) ([]any, bool, error) {
			return fixture(), true, nil
		})
	}
	handler.runner.importFn = func(ctx context.Context, cityID uuid.UUID, source sourceconfig.Source, secret string, syncStore providers.SyncStore) (providers.ImportStats, error) {
		return importRecordsWithFetcher(ctx, cityID, source, secret, syncStore, func(context.Context, generic.HTTPConfig, generic.PaginationConfig) ([]any, int, error) {
			return fixture(), 1, nil
		})
	}

	frontendDir, err := filepath.Abs(filepath.Join("..", "..", "..", "frontend"))
	if err != nil {
		t.Fatal(err)
	}
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer buildCancel()
	build := exec.CommandContext(buildCtx, "npm", "run", "build")
	build.Dir = frontendDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build frontend for browser test: %v\n%s", err, output)
	}
	distDir := filepath.Join(frontendDir, "dist")
	if _, err := os.Stat(filepath.Join(distDir, "index.html")); err != nil {
		t.Fatalf("frontend build output is unavailable: %v", err)
	}

	router := chi.NewRouter()
	const testAccessToken = "browser-fixture-access-token"
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: userID})))
		})
	}
	handler.RegisterRoutes(router)

	// Browser-facing product routes use the production handlers and PostgreSQL
	// repositories. Only authentication is replaced with a deterministic test
	// principal; this router exists solely inside this integration test.
	discoveryRepository := discovery.NewRepository(db)
	cursorCodec, err := discovery.NewCursorCodec(bytes.Repeat([]byte{0x43}, 32))
	if err != nil {
		t.Fatal(err)
	}
	discoveryService := discovery.NewService(discoveryRepository, cursorCodec)
	preferencesService := preferences.NewService(db)
	discovery.NewDetailHandler(discoveryService).RegisterRoutes(router, authenticate)
	discovery.NewSearchHandler(discoveryService, discoveryRepository).RegisterRoutes(router, authenticate)
	homeService := discovery.NewHomeService(discoveryService, discoveryRepository, preferencesService, discoveryRepository)
	discovery.NewHomeHandler(homeService).RegisterRoutes(router, authenticate)
	ticketService, err := tickets.NewService(db, behavior.Recorder{}, append(providerpolicy.Defaults().Tickets, "tickets.example.test"))
	if err != nil {
		t.Fatal(err)
	}
	tickets.NewHandler(ticketService).RegisterRoutes(router, authenticate)
	fixturePNG := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 31, 21, 196, 137, 0, 0, 0, 13, 73, 68, 65, 84, 120, 156, 99, 248, 207, 192, 240, 31, 0, 5, 0, 1, 255, 137, 153, 61, 29, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130}
	eventresources.NewHandlerWithFetcher(db, func(context.Context, string) ([]byte, string, error) {
		return fixturePNG, "image/png", nil
	}).RegisterRoutes(router)
	router.Post("/api/v1/auth/max/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"` + testAccessToken + `","token_type":"Bearer","expires_in":3600,"user":{"id":"` + userID.String() + `","display_name":"Browser fixture user","avatar_url":null,"city_id":"` + moscowID.String() + `","locale":"ru-RU"},"onboarding_state":"complete","preferences":null,"daily_notifications_enabled":false,"invite_context":null,"shared_event_id":null}`))
	})
	router.Get("/__test__/generic-event", func(w http.ResponseWriter, r *http.Request) {
		var id uuid.UUID
		if err := db.QueryRow(r.Context(), `SELECT id FROM events WHERE source=$1 AND external_id=$2`, sourceKey, externalID).Scan(&id); err != nil {
			http.NotFound(w, r)
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, map[string]string{"event_id": id.String()})
	})
	fileServer := http.FileServer(http.Dir(distDir))
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if filepath.Ext(r.URL.Path) != "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	browserCtx, browserCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer browserCancel()
	browser := exec.CommandContext(browserCtx, "npx", "playwright", "test", "--config", "playwright.generic.config.ts", "tests/e2e/event-sources-live.spec.ts")
	browser.Dir = frontendDir
	browser.Env = append(os.Environ(), "GENERIC_E2E_BASE_URL="+server.URL, "GENERIC_E2E_SOURCE_KEY="+sourceKey, "GENERIC_E2E_SOURCE_NAME=Browser Source "+suffix)
	if output, err := browser.CombinedOutput(); err != nil {
		t.Fatalf("live backend browser scenario: %v\n%s", err, output)
	}
}

func openEventSourcesTestDB(t *testing.T) *store.Pool {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open event source test database: %v", err)
	}
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		db.Close()
		t.Fatalf("check event source test migrations: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func doHandlerRequest(t *testing.T, handler http.Handler, method, path string, body []byte, mutation bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "https://admin.example.test"+path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if mutation {
		request.Header.Set("X-Admin-Request", "1")
		request.Header.Set("Origin", "https://admin.example.test")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func handlerTestInput(sourceKey, secret, externalID string) sourceconfig.Input {
	return sourceconfig.Input{
		SourceKey: sourceKey, Name: "Generic integration fixture", Enabled: true,
		EndpointURL: "https://public.example.test/events", AuthType: "bearer", Secret: secret,
		QueryParams: map[string]string{"fixture": "yes"}, ResponsePath: "",
		Pagination: json.RawMessage(`{"mode":"none"}`),
		Mapping:    json.RawMessage(`{"external_id":{"path":"id"},"title":{"path":"title"},"starts_at":{"path":"starts_at"},"venue_name":{"path":"venue.name"},"venue_address":{"path":"venue.address"},"latitude":{"path":"venue.latitude"},"longitude":{"path":"venue.longitude"},"ticket_url":{"path":"ticket_url"},"ticket_available":{"path":"ticket_available"},"price_from":{"path":"price","transform":"number"}}`),
		Defaults:   sourceconfig.Defaults{Category: "concerts", Timezone: "UTC", Currency: "RUB", Status: providers.EventStatusPublished},
		PriceUnit:  "major",
	}
}

func handlerTestRecord(externalID string) any {
	return map[string]any{
		"id": externalID, "title": "Generic handler fixture",
		"starts_at":  time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339),
		"venue":      map[string]any{"name": "Handler fixture venue", "address": "1 Test Way", "latitude": 55.75, "longitude": 37.62},
		"ticket_url": "https://tickets.example/handler-fixture", "ticket_available": true, "price": 10.0,
	}
}
