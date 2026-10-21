package eventsubmissions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/adminauth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

// This integration test exercises the PostgreSQL-only timestamp arithmetic in
// the moderation detail query. It covers both duplicate-result branches and
// the resource-domain data returned to moderators.
func TestModerationDetailPostgres(t *testing.T) {
	db := openModerationDetailTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cityID, authorID, submissionID, venueID, eventID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	domainID := uuid.New()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = db.Exec(cleanupCtx, `DELETE FROM event_submissions WHERE id=$1`, submissionID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM events WHERE id=$1`, eventID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM venues WHERE id=$1`, venueID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM event_source_allowed_domains WHERE id=$1`, domainID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, authorID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM cities WHERE id=$1`, cityID)
	})
	startsAt := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)
	startsLocal := startsAt.Format("2006-01-02T15:04")
	input := SubmissionInput{
		Title:       "Moderation detail integration " + submissionID.String(),
		Description: "Integration fixture", CityID: cityID,
		VenueName: "Integration venue " + venueID.String(), VenueAddress: "Fixture street",
		StartsAtLocal: startsLocal, PriceMode: "free", CategorySlug: "other",
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'UTC',0,0)`, cityID, "Moderation fixture"); err != nil {
		t.Fatalf("insert fixture city: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO users(id,max_user_id,display_name) VALUES($1,$2,$3)`, authorID, time.Now().UnixNano(), "Moderation fixture"); err != nil {
		t.Fatalf("insert fixture author: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO event_submissions(id,author_id,city_id,starts_at,timezone,payload,status,idempotency_key,request_hash) VALUES($1,$2,$3,$4,'UTC',$5,'pending',$6,$7)`, submissionID, authorID, cityID, startsAt, payload, submissionID.String(), make([]byte, 32)); err != nil {
		t.Fatalf("insert fixture submission: %v", err)
	}
	domainHost := "moderation-" + strings.ReplaceAll(submissionID.String(), "-", "") + ".example.test"
	h := NewHandlerWithFetcher(db, func(context.Context, string) ([]byte, string, error) { return nil, "", nil })
	router := chi.NewRouter()
	router.Use(adminauth.NewMiddleware("moderation-detail-test-secret"))
	h.RegisterRoutes(router, nil)
	detailPath := "/api/v1/internal/event-submissions/" + submissionID.String()

	// No duplicate rows must still return a valid detail response.
	assertModerationDetail(t, router, detailPath, submissionID, domainHost, 0, false)
	if _, err := db.Exec(ctx, `INSERT INTO event_source_allowed_domains(id,source_id,hostname,purpose,enabled) VALUES($1,$2,$3,'image',true)`, domainID, uuid.MustParse(UserSourceID), domainHost); err != nil {
		t.Fatalf("insert fixture resource domain: %v", err)
	}

	if _, err := db.Exec(ctx, `INSERT INTO venues(id,city_id,name,address,latitude,longitude) VALUES($1,$2,$3,'Fixture street',0,0)`, venueID, cityID, input.VenueName); err != nil {
		t.Fatalf("insert fixture venue: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO events(id,source,external_id,title,description,venue_id,starts_at,timezone,status) VALUES($1,'integration',$2,$3,'Fixture event',$4,$5,'UTC','published')`, eventID, eventID.String(), input.Title, venueID, startsAt.Add(time.Hour)); err != nil {
		t.Fatalf("insert fixture duplicate event: %v", err)
	}
	assertModerationDetail(t, router, detailPath, submissionID, domainHost, 1, true)

	missing := httptest.NewRecorder()
	serveModerationDetailRequest(router, missing, "/api/v1/internal/event-submissions/"+uuid.NewString())
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown detail status=%d, want 404: %s", missing.Code, missing.Body.String())
	}
}

type moderationDetailResponse struct {
	Submission Submission `json:"submission"`
	Duplicates []struct {
		EventID uuid.UUID `json:"event_id"`
		Title   string    `json:"title"`
	} `json:"duplicates"`
	ResourceDomains []struct {
		Hostname string `json:"hostname"`
		Purpose  string `json:"purpose"`
	} `json:"resource_domains"`
}

func assertModerationDetail(t *testing.T, handler http.Handler, path string, submissionID uuid.UUID, domainHost string, wantDuplicates int, wantDomain bool) {
	t.Helper()
	response := httptest.NewRecorder()
	serveModerationDetailRequest(handler, response, path)
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d, want 200: %s", response.Code, response.Body.String())
	}
	var body moderationDetailResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if body.Submission.ID != submissionID || body.Submission.Status != "pending" {
		t.Fatalf("submission=%+v, want pending %s", body.Submission, submissionID)
	}
	if len(body.Duplicates) != wantDuplicates {
		t.Fatalf("duplicates=%d, want %d: %+v", len(body.Duplicates), wantDuplicates, body.Duplicates)
	}
	if !wantDomain {
		if len(body.ResourceDomains) != 0 {
			t.Fatalf("resource_domains=%+v, want empty", body.ResourceDomains)
		}
	} else if len(body.ResourceDomains) != 1 || body.ResourceDomains[0].Hostname != domainHost || body.ResourceDomains[0].Purpose != "image" {
		t.Fatalf("resource_domains=%+v, want image domain %q", body.ResourceDomains, domainHost)
	}
}

func serveModerationDetailRequest(handler http.Handler, response *httptest.ResponseRecorder, path string) {
	request := httptest.NewRequest(http.MethodGet, "https://admin.example.test"+path, nil)
	request.Header.Set("X-Internal-Admin-Secret", "moderation-detail-test-secret")
	request.Header.Set("X-Internal-Admin-User", "integration-test-admin")
	handler.ServeHTTP(response, request)
}

func openModerationDetailTestDB(t *testing.T) *store.Pool {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open moderation detail test database: %v", err)
	}
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		db.Close()
		t.Fatalf("check moderation detail test migrations: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}
