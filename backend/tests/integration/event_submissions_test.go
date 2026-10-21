package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/adminauth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventresources"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventsubmissions"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventuploads"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/resourcedomains"
)

func TestEventSubmissionIdempotencyAndAtomicApproval(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db := openTestDB(t)
	ctx := context.Background()
	cityID, userID := uuid.New(), uuid.New()
	var submittedID, publishedEventID uuid.UUID
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'Europe/Moscow',55.75,37.61)`, cityID, "Submission test "+cityID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO users(id,max_user_id,display_name) VALUES($1,$2,'Submission author')`, userID, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM event_submissions WHERE id=$1`, submittedID)
		_, _ = db.Exec(context.Background(), `DELETE FROM events WHERE id=$1`, publishedEventID)
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
		_, _ = db.Exec(context.Background(), `DELETE FROM venues WHERE city_id=$1 AND NOT EXISTS (SELECT 1 FROM events WHERE events.venue_id=venues.id)`, cityID)
		_, _ = db.Exec(context.Background(), `DELETE FROM cities WHERE id=$1`, cityID)
	})

	userAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: userID})))
		})
	}
	router := chi.NewRouter()
	eventsubmissions.NewHandler(db).RegisterRoutes(router, userAuth)
	handler := adminauth.NewMiddleware("integration-secret")(router)

	start := time.Now().In(time.FixedZone("MSK", 3*60*60)).Add(48 * time.Hour).Truncate(time.Minute).Format("2006-01-02T15:04")
	body, _ := json.Marshal(map[string]any{"title": "A user event", "description": "Community event", "city_id": cityID, "venue_name": "New venue", "venue_address": "1 Example street", "starts_at_local": start, "price_mode": "free", "category_slug": "concerts"})
	key := "submission-" + uuid.NewString()
	first := eventSubmissionRequest(t, handler, http.MethodPost, "/api/v1/event-submissions", body, map[string]string{"Idempotency-Key": key})
	if first.Code != http.StatusCreated {
		t.Fatalf("submit status=%d body=%s", first.Code, first.Body.String())
	}
	var created struct {
		ID     uuid.UUID `json:"id"`
		Status string    `json:"status"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil || created.ID == uuid.Nil || created.Status != "pending" {
		t.Fatalf("submit response=%s err=%v", first.Body.String(), err)
	}
	submittedID = created.ID
	replay := eventSubmissionRequest(t, handler, http.MethodPost, "/api/v1/event-submissions", body, map[string]string{"Idempotency-Key": key})
	var replayed struct {
		ID uuid.UUID `json:"id"`
	}
	if replay.Code != http.StatusCreated || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || replayed.ID != created.ID {
		t.Fatalf("idempotent replay=%d %s", replay.Code, replay.Body.String())
	}
	conflictBody := bytes.Replace(body, []byte("A user event"), []byte("Another event"), 1)
	conflict := eventSubmissionRequest(t, handler, http.MethodPost, "/api/v1/event-submissions", conflictBody, map[string]string{"Idempotency-Key": key})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict=%d %s", conflict.Code, conflict.Body.String())
	}

	approvePath := "/api/v1/internal/event-submissions/" + created.ID.String() + "/approve"
	adminHeaders := map[string]string{"X-Internal-Admin-Secret": "integration-secret", "X-Internal-Admin-User": "moderator", "X-Admin-Request": "1", "Origin": "https://test.local", "Host": "test.local"}
	approved := eventSubmissionRequest(t, handler, http.MethodPost, approvePath, []byte(`{}`), adminHeaders)
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	var eventResult struct {
		EventID uuid.UUID `json:"event_id"`
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &eventResult); err != nil || eventResult.EventID == uuid.Nil {
		t.Fatalf("approval response=%s err=%v", approved.Body.String(), err)
	}
	publishedEventID = eventResult.EventID
	again := eventSubmissionRequest(t, handler, http.MethodPost, approvePath, []byte(`{}`), adminHeaders)
	var repeated struct {
		EventID uuid.UUID `json:"event_id"`
	}
	if again.Code != http.StatusOK || json.Unmarshal(again.Body.Bytes(), &repeated) != nil || repeated.EventID != eventResult.EventID {
		t.Fatalf("approval replay=%d %s", again.Code, again.Body.String())
	}
	var eventCount int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM events WHERE id=$1 AND source='user' AND external_id=$2 AND status='published' AND provider_active AND NOT is_demo`, eventResult.EventID, created.ID.String()).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("published event count=%d err=%v", eventCount, err)
	}
	var coordinatesNull bool
	if err := db.QueryRow(ctx, `SELECT latitude IS NULL AND longitude IS NULL FROM venues WHERE id=(SELECT venue_id FROM events WHERE id=$1)`, eventResult.EventID).Scan(&coordinatesNull); err != nil || !coordinatesNull {
		t.Fatalf("new venue coordinates null=%t err=%v", coordinatesNull, err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM event_submissions WHERE id=$1`, submittedID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM events WHERE id=$1`, publishedEventID); err != nil {
		t.Fatal(err)
	}
}

func TestSubmissionImageRequiresAndRechecksDomainApproval(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db := openTestDB(t)
	ctx := context.Background()
	cityID, userID, submissionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'Europe/Moscow',55.75,37.61)`, cityID, "Poster test "+cityID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO users(id,max_user_id,display_name) VALUES($1,$2,'Poster author')`, userID, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	imageURL := "https://images.submission.test/poster.png"
	start := time.Now().Add(48 * time.Hour).In(time.FixedZone("MSK", 3*60*60)).Truncate(time.Minute)
	input := eventsubmissions.SubmissionInput{Title: "Poster event", Description: "Details", CityID: cityID, VenueName: "Hall", VenueAddress: "Street", StartsAtLocal: start.Format("2006-01-02T15:04"), PriceMode: "free", CategorySlug: "concerts", ImageURL: &imageURL}
	payload, _ := json.Marshal(input)
	hash := sha256.Sum256([]byte("integration-poster"))
	if _, err := db.Exec(ctx, `INSERT INTO event_submissions(id,author_id,city_id,starts_at,timezone,payload,status,idempotency_key,request_hash) VALUES($1,$2,$3,$4,'Europe/Moscow',$5,'pending',$6,$7)`, submissionID, userID, cityID, start, payload, "poster-"+submissionID.String(), hash[:]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM event_submissions WHERE id=$1`, submissionID)
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
		_, _ = db.Exec(context.Background(), `DELETE FROM cities WHERE id=$1`, cityID)
	})
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	handler := eventsubmissions.NewHandlerWithFetcher(db, func(_ context.Context, got string) ([]byte, string, error) {
		calls++
		if got != imageURL {
			t.Errorf("fetch URL=%q", got)
		}
		close(started)
		<-release
		return []byte("image"), "image/png", nil
	})
	router := chi.NewRouter()
	handler.RegisterRoutes(router, nil)
	httpHandler := adminauth.NewMiddleware("integration-secret")(router)
	path := "/api/v1/internal/event-submissions/" + submissionID.String() + "/image"
	headers := map[string]string{"X-Internal-Admin-Secret": "integration-secret", "X-Internal-Admin-User": "moderator"}
	unapproved := eventSubmissionRequest(t, httpHandler, http.MethodGet, path, nil, headers)
	if unapproved.Code != http.StatusForbidden || calls != 0 {
		t.Fatalf("unapproved image status=%d calls=%d body=%s", unapproved.Code, calls, unapproved.Body.String())
	}
	repository, err := resourcedomains.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := repository.Add(ctx, uuid.MustParse(eventsubmissions.UserSourceID), "images.submission.test", resourcedomains.PurposeImage)
	if err != nil {
		t.Fatal(err)
	}
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- eventSubmissionRequest(t, httpHandler, http.MethodGet, path, nil, headers) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("image fetch did not start")
	}
	if revoked, err := repository.Revoke(ctx, uuid.MustParse(eventsubmissions.UserSourceID), domain.ID); err != nil || !revoked {
		t.Fatalf("revoke=(%t,%v)", revoked, err)
	}
	close(release)
	result := <-response
	if result.Code != http.StatusForbidden {
		t.Fatalf("revoked in-flight image status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestUploadedPosterClaimApprovalAndPublishedServing(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db := openTestDB(t)
	ctx := context.Background()
	cityID, authorID, otherID, submissionID, eventID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.Nil
	for _, user := range []struct {
		id   uuid.UUID
		name string
	}{{authorID, "Poster author"}, {otherID, "Other author"}} {
		if _, err := db.Exec(ctx, `INSERT INTO users(id,max_user_id,display_name) VALUES($1,$2,$3)`, user.id, time.Now().UnixNano(), user.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'Europe/Moscow',55.75,37.61)`, cityID, "Uploaded poster test "+cityID.String()); err != nil {
		t.Fatal(err)
	}
	objects := &memoryObjectStore{items: map[string]memoryObject{}}
	uploads := eventuploads.NewService(db, objects)
	upload, err := uploads.Store(ctx, authorID, []byte("valid-test-poster"), "image/png", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM events WHERE id=$1`, eventID)
		_, _ = db.Exec(context.Background(), `DELETE FROM event_submissions WHERE id=$1`, submissionID)
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1,$2)`, authorID, otherID)
		_, _ = db.Exec(context.Background(), `DELETE FROM venues WHERE city_id=$1 AND NOT EXISTS (SELECT 1 FROM events WHERE events.venue_id=venues.id)`, cityID)
		_, _ = db.Exec(context.Background(), `DELETE FROM cities WHERE id=$1`, cityID)
	})

	currentUser := authorID
	userAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: currentUser})))
		})
	}
	router := chi.NewRouter()
	moderation := eventsubmissions.NewHandlerWithUploads(db, func(context.Context, string) ([]byte, string, error) {
		return nil, "", http.ErrNotSupported
	}, uploads)
	moderation.RegisterRoutes(router, userAuth)
	eventresources.NewHandlerWithUploads(db, func(context.Context, string) ([]byte, string, error) {
		return nil, "", http.ErrNotSupported
	}, uploads).RegisterRoutes(router)
	handler := adminauth.NewMiddleware("integration-secret")(router)

	start := time.Now().In(time.FixedZone("MSK", 3*60*60)).Add(48 * time.Hour).Truncate(time.Minute).Format("2006-01-02T15:04")
	body, _ := json.Marshal(map[string]any{"title": "Uploaded poster event", "description": "Community event", "city_id": cityID, "venue_name": "Poster Hall", "venue_address": "1 Example street", "starts_at_local": start, "price_mode": "free", "category_slug": "concerts", "image_id": upload.ID})
	key := "uploaded-poster-" + uuid.NewString()
	currentUser = otherID
	unauthorizedOwner := eventSubmissionRequest(t, handler, http.MethodPost, "/api/v1/event-submissions", body, map[string]string{"Idempotency-Key": key})
	if unauthorizedOwner.Code != http.StatusUnprocessableEntity {
		t.Fatalf("foreign upload status=%d body=%s", unauthorizedOwner.Code, unauthorizedOwner.Body.String())
	}
	currentUser = authorID
	created := eventSubmissionRequest(t, handler, http.MethodPost, "/api/v1/event-submissions", body, map[string]string{"Idempotency-Key": key})
	if created.Code != http.StatusCreated {
		t.Fatalf("submit uploaded poster status=%d body=%s", created.Code, created.Body.String())
	}
	var result struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || result.ID == uuid.Nil {
		t.Fatalf("submission response=%s err=%v", created.Body.String(), err)
	}
	submissionID = result.ID
	var claimed *uuid.UUID
	if err := db.QueryRow(ctx, `SELECT submission_id FROM event_image_uploads WHERE id=$1`, upload.ID).Scan(&claimed); err != nil || claimed == nil || *claimed != submissionID {
		t.Fatalf("claimed submission=%v err=%v", claimed, err)
	}
	reuse := eventSubmissionRequest(t, handler, http.MethodPost, "/api/v1/event-submissions", body, map[string]string{"Idempotency-Key": key + "-reuse"})
	if reuse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reused upload status=%d body=%s", reuse.Code, reuse.Body.String())
	}
	adminHeaders := map[string]string{"X-Internal-Admin-Secret": "integration-secret", "X-Internal-Admin-User": "moderator", "X-Admin-Request": "1", "Origin": "https://test.local", "Host": "test.local"}
	approved := eventSubmissionRequest(t, handler, http.MethodPost, "/api/v1/internal/event-submissions/"+submissionID.String()+"/approve", []byte(`{}`), adminHeaders)
	if approved.Code != http.StatusOK {
		t.Fatalf("approve uploaded poster status=%d body=%s", approved.Code, approved.Body.String())
	}
	var approval struct {
		EventID uuid.UUID `json:"event_id"`
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &approval); err != nil || approval.EventID == uuid.Nil {
		t.Fatalf("approval response=%s err=%v", approved.Body.String(), err)
	}
	eventID = approval.EventID
	var imageWidth, imageHeight int
	if err := db.QueryRow(ctx, `SELECT width,height FROM event_images WHERE id=$1 AND event_id=$2`, upload.ID, eventID).Scan(&imageWidth, &imageHeight); err != nil || imageWidth != 2 || imageHeight != 3 {
		t.Fatalf("published image dimensions=%dx%d err=%v", imageWidth, imageHeight, err)
	}
	moderatorImage := eventSubmissionRequest(t, handler, http.MethodGet, "/api/v1/internal/event-submissions/"+submissionID.String()+"/image", nil, adminHeaders)
	if moderatorImage.Code != http.StatusOK || moderatorImage.Body.String() != "valid-test-poster" {
		t.Fatalf("moderator poster status=%d body=%q", moderatorImage.Code, moderatorImage.Body.String())
	}
	publicImage := eventSubmissionRequest(t, handler, http.MethodGet, "/api/v1/event-images/"+upload.ID.String()+"/content", nil, nil)
	if publicImage.Code != http.StatusOK || publicImage.Body.String() != "valid-test-poster" || publicImage.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("published poster status=%d headers=%v body=%q", publicImage.Code, publicImage.Header(), publicImage.Body.String())
	}
}

type memoryObject struct {
	body        []byte
	contentType string
}
type memoryObjectStore struct {
	mu    sync.Mutex
	items map[string]memoryObject
}

func (s *memoryObjectStore) Available() bool { return s != nil }
func (s *memoryObjectStore) Put(_ context.Context, key string, body []byte, contentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = memoryObject{body: append([]byte(nil), body...), contentType: contentType}
	return nil
}
func (s *memoryObjectStore) Get(_ context.Context, key string) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[key]
	if !ok {
		return nil, "", os.ErrNotExist
	}
	return append([]byte(nil), item.body...), item.contentType, nil
}
func (s *memoryObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
	return nil
}

func eventSubmissionRequest(t *testing.T, handler http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for name, value := range headers {
		if name == "Host" {
			req.Host = value
		} else {
			req.Header.Set(name, value)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}
