package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/adminauth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventsubmissions"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

type submissionAPI struct {
	handler http.Handler
	cityID  uuid.UUID
	userIDs []uuid.UUID
}

// newSubmissionAPI creates isolated principals and routes for these tests.
// User identity is test-controlled by X-Test-User so ownership can be checked
// with the same public route and independent authenticated principals.
func newSubmissionAPI(t *testing.T) (*submissionAPI, *store.Pool) {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	cityID := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'Europe/Moscow',55.75,37.61)`, cityID, "Submission concurrency "+cityID.String()); err != nil {
		t.Fatal(err)
	}
	userIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for i, id := range userIDs {
		if _, err := db.Exec(ctx, `INSERT INTO users(id,max_user_id,display_name) VALUES($1,$2,$3)`, id, time.Now().UnixNano()+int64(i), "Submission tester"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range userIDs {
			_, _ = db.Exec(context.Background(), `DELETE FROM event_submissions WHERE author_id=$1`, id)
		}
		_, _ = db.Exec(context.Background(), `DELETE FROM events WHERE source='user' AND venue_id IN (SELECT id FROM venues WHERE city_id=$1)`, cityID)
		_, _ = db.Exec(context.Background(), `DELETE FROM venues WHERE city_id=$1 AND NOT EXISTS (SELECT 1 FROM events WHERE events.venue_id=venues.id)`, cityID)
		for _, id := range userIDs {
			_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id)
		}
		_, _ = db.Exec(context.Background(), `DELETE FROM cities WHERE id=$1`, cityID)
	})

	router := chi.NewRouter()
	userAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := uuid.Parse(r.Header.Get("X-Test-User"))
			if err != nil || id == uuid.Nil {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: id})))
		})
	}
	eventsubmissions.NewHandler(db).RegisterRoutes(router, userAuth)
	return &submissionAPI{handler: adminauth.NewMiddleware("integration-secret")(router), cityID: cityID, userIDs: userIDs}, db
}

func (api *submissionAPI) request(t *testing.T, user uuid.UUID, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	if headers == nil {
		headers = map[string]string{}
	}
	if user != uuid.Nil {
		headers["X-Test-User"] = user.String()
	}
	return eventSubmissionRequest(t, api.handler, method, path, body, headers)
}

func (api *submissionAPI) submit(t *testing.T, user uuid.UUID, key, venue string) uuid.UUID {
	t.Helper()
	start := time.Now().In(time.FixedZone("MSK", 3*60*60)).Add(48 * time.Hour).Truncate(time.Minute).Format("2006-01-02T15:04")
	body, err := json.Marshal(map[string]any{
		"title": "Concurrent community event", "description": "A stable fixture event", "city_id": api.cityID,
		"venue_name": venue, "venue_address": "1 Example street", "starts_at_local": start,
		"price_mode": "free", "category_slug": "concerts",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := api.request(t, user, http.MethodPost, "/api/v1/event-submissions", body, map[string]string{"Idempotency-Key": key})
	if response.Code != http.StatusCreated {
		t.Fatalf("submit status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.ID == uuid.Nil {
		t.Fatalf("submit response=%s err=%v", response.Body.String(), err)
	}
	return created.ID
}

func adminHeaders() map[string]string {
	return map[string]string{"X-Internal-Admin-Secret": "integration-secret", "X-Internal-Admin-User": "fixture-moderator", "X-Admin-Request": "1", "Origin": "https://test.local", "Host": "test.local"}
}

func TestConcurrentApprovalReturnsSingleStableEvent(t *testing.T) {
	api, db := newSubmissionAPI(t)
	submissionID := api.submit(t, api.userIDs[0], "concurrent-"+uuid.NewString(), "Concurrent Hall")
	path := "/api/v1/internal/event-submissions/" + submissionID.String() + "/approve"
	responses := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			responses[i] = eventSubmissionRequest(t, api.handler, http.MethodPost, path, []byte(`{}`), adminHeaders())
		}(i)
	}
	close(start)
	wg.Wait()
	var eventIDs []uuid.UUID
	for _, response := range responses {
		if response.Code != http.StatusOK {
			t.Fatalf("concurrent approval status=%d body=%s", response.Code, response.Body.String())
		}
		var result struct {
			EventID uuid.UUID `json:"event_id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.EventID == uuid.Nil {
			t.Fatalf("approval response=%s err=%v", response.Body.String(), err)
		}
		eventIDs = append(eventIDs, result.EventID)
	}
	if eventIDs[0] != eventIDs[1] {
		t.Fatalf("concurrent approvals returned different event IDs: %s and %s", eventIDs[0], eventIDs[1])
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE source='user' AND external_id=$1`, submissionID.String()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("published event count=%d err=%v; want exactly one", count, err)
	}
}

func TestConcurrentApproveRejectHasOneTerminalDecision(t *testing.T) {
	api, db := newSubmissionAPI(t)
	submissionID := api.submit(t, api.userIDs[0], "decision-race-"+uuid.NewString(), "Decision Hall")
	base := "/api/v1/internal/event-submissions/" + submissionID.String()
	responses := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		responses[0] = eventSubmissionRequest(t, api.handler, http.MethodPost, base+"/approve", []byte(`{}`), adminHeaders())
	}()
	go func() {
		defer wg.Done()
		<-start
		responses[1] = eventSubmissionRequest(t, api.handler, http.MethodPost, base+"/reject", []byte(`{"reason":"Not suitable"}`), adminHeaders())
	}()
	close(start)
	wg.Wait()
	statuses := []int{responses[0].Code, responses[1].Code}
	if !((statuses[0] == http.StatusOK && statuses[1] == http.StatusConflict) || (statuses[0] == http.StatusConflict && statuses[1] == http.StatusOK)) {
		t.Fatalf("approve/reject statuses=%v; want one success and one conflict; bodies=%q %q", statuses, responses[0].Body.String(), responses[1].Body.String())
	}
	var status string
	if err := db.QueryRow(context.Background(), `SELECT status FROM event_submissions WHERE id=$1`, submissionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE source='user' AND external_id=$1`, submissionID.String()).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if (status == "approved" && events != 1) || (status == "rejected" && events != 0) {
		t.Fatalf("terminal status=%q published events=%d", status, events)
	}
}

func TestApprovalRollsBackVenueAndEventWhenSubmissionDecisionWriteFails(t *testing.T) {
	api, db := newSubmissionAPI(t)
	userID := api.userIDs[0]
	submissionID := api.submit(t, userID, "rollback-"+uuid.NewString(), "Rollback Only Hall")
	suffix := strings.ReplaceAll(userID.String(), "-", "")
	functionName, triggerName := "fail_sub_decision_"+suffix, "fail_sub_dec_trigger_"+suffix
	if _, err := db.Exec(context.Background(), fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.author_id = '%s'::uuid THEN RAISE EXCEPTION 'fixture decision write failure'; END IF; RETURN NEW; END $$`, functionName, userID)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON event_submissions FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		_, _ = db.Exec(context.Background(), "DROP FUNCTION "+functionName+"()")
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), "DROP TRIGGER IF EXISTS "+triggerName+" ON event_submissions")
		_, _ = db.Exec(context.Background(), "DROP FUNCTION IF EXISTS "+functionName+"()")
	})

	path := "/api/v1/internal/event-submissions/" + submissionID.String() + "/approve"
	response := eventSubmissionRequest(t, api.handler, http.MethodPost, path, []byte(`{}`), adminHeaders())
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("approval status=%d body=%s; want injected failure", response.Code, response.Body.String())
	}
	var status string
	var eventID *uuid.UUID
	if err := db.QueryRow(context.Background(), `SELECT status,event_id FROM event_submissions WHERE id=$1`, submissionID).Scan(&status, &eventID); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || eventID != nil {
		t.Fatalf("submission after rollback status=%q event_id=%v", status, eventID)
	}
	var events, venues int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE source='user' AND external_id=$1`, submissionID.String()).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM venues WHERE city_id=$1 AND name='Rollback Only Hall'`, api.cityID).Scan(&venues); err != nil {
		t.Fatal(err)
	}
	if events != 0 || venues != 0 {
		t.Fatalf("rollback left events=%d venues=%d", events, venues)
	}
}

func TestSubmissionOwnershipMissingSessionAdminGuardAndQuotaReplay(t *testing.T) {
	api, _ := newSubmissionAPI(t)
	userA, userB := api.userIDs[0], api.userIDs[1]
	body, _ := json.Marshal(map[string]any{
		"title": "Owned event", "description": "A user event", "city_id": api.cityID,
		"venue_name": "Ownership Hall", "venue_address": "1 Example street",
		"starts_at_local": time.Now().In(time.FixedZone("MSK", 3*60*60)).Add(48 * time.Hour).Truncate(time.Minute).Format("2006-01-02T15:04"),
		"price_mode":      "free", "category_slug": "concerts",
	})
	missingSession := api.request(t, uuid.Nil, http.MethodPost, "/api/v1/event-submissions", body, map[string]string{"Idempotency-Key": "missing-session"})
	if missingSession.Code != http.StatusUnauthorized {
		t.Fatalf("missing session status=%d body=%s", missingSession.Code, missingSession.Body.String())
	}
	adminUntrusted := eventSubmissionRequest(t, api.handler, http.MethodGet, "/api/v1/internal/event-submissions", nil, nil)
	if adminUntrusted.Code != http.StatusForbidden {
		t.Fatalf("unguarded internal route status=%d body=%s", adminUntrusted.Code, adminUntrusted.Body.String())
	}

	firstID := api.submit(t, userA, "ownership-first-"+uuid.NewString(), "Ownership First")
	secondID := api.submit(t, userB, "ownership-second-"+uuid.NewString(), "Ownership Second")
	listA := api.request(t, userA, http.MethodGet, "/api/v1/me/event-submissions", nil, nil)
	listB := api.request(t, userB, http.MethodGet, "/api/v1/me/event-submissions", nil, nil)
	for user, scenario := range map[string]struct {
		response *httptest.ResponseRecorder
		own      uuid.UUID
		foreign  uuid.UUID
	}{"author A": {listA, firstID, secondID}, "author B": {listB, secondID, firstID}} {
		var result struct {
			Items []struct {
				ID uuid.UUID `json:"id"`
			} `json:"items"`
		}
		if scenario.response.Code != http.StatusOK || json.Unmarshal(scenario.response.Body.Bytes(), &result) != nil {
			t.Fatalf("%s list status=%d body=%s", user, scenario.response.Code, scenario.response.Body.String())
		}
		foundOwn, foundForeign := false, false
		for _, item := range result.Items {
			foundOwn = foundOwn || item.ID == scenario.own
			foundForeign = foundForeign || item.ID == scenario.foreign
		}
		if !foundOwn || foundForeign {
			t.Fatalf("%s list ownership incorrect: %+v", user, result.Items)
		}
	}

	quotaAPI, quotaDB := newSubmissionAPI(t)
	quotaUser := quotaAPI.userIDs[0]
	quotaKey := "quota-replay-" + uuid.NewString()
	var replayID uuid.UUID
	quotaBody := submissionBodyForCity(t, quotaAPI.cityID, "Quota Hall")
	for i := 0; i < 5; i++ {
		response := quotaAPI.request(t, quotaUser, http.MethodPost, "/api/v1/event-submissions", quotaBody, map[string]string{"Idempotency-Key": quotaKeyFor(quotaKey, i)})
		if response.Code != http.StatusCreated {
			t.Fatalf("quota submission %d status=%d body=%s", i, response.Code, response.Body.String())
		}
		var created struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.ID == uuid.Nil {
			t.Fatalf("quota submission %d response=%s err=%v", i, response.Body.String(), err)
		}
		if i == 0 {
			replayID = created.ID
		}
	}
	tooMany := quotaAPI.request(t, quotaUser, http.MethodPost, "/api/v1/event-submissions", submissionBodyForCity(t, quotaAPI.cityID, "Overflow Hall"), map[string]string{"Idempotency-Key": quotaKey + "-overflow"})
	if tooMany.Code != http.StatusTooManyRequests {
		t.Fatalf("sixth submission status=%d body=%s", tooMany.Code, tooMany.Body.String())
	}
	replay := quotaAPI.request(t, quotaUser, http.MethodPost, "/api/v1/event-submissions", quotaBody, map[string]string{"Idempotency-Key": quotaKeyFor(quotaKey, 0)})
	var replayResult struct {
		ID uuid.UUID `json:"id"`
	}
	if replay.Code != http.StatusCreated || json.Unmarshal(replay.Body.Bytes(), &replayResult) != nil || replayResult.ID != replayID {
		t.Fatalf("quota replay status=%d body=%s; expected original ID %s", replay.Code, replay.Body.String(), replayID)
	}
	var submissionCount int
	if err := quotaDB.QueryRow(context.Background(), `SELECT count(*) FROM event_submissions WHERE author_id=$1`, quotaUser).Scan(&submissionCount); err != nil || submissionCount != 5 {
		t.Fatalf("submission count after replay=%d err=%v; want 5", submissionCount, err)
	}
}

func quotaKeyFor(base string, index int) string { return fmt.Sprintf("%s-%d", base, index) }

func submissionBodyForCity(t *testing.T, cityID uuid.UUID, venue string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"title": "Concurrent community event", "description": "A stable fixture event", "city_id": cityID,
		"venue_name": venue, "venue_address": "1 Example street",
		"starts_at_local": time.Now().In(time.FixedZone("MSK", 3*60*60)).Add(48 * time.Hour).Truncate(time.Minute).Format("2006-01-02T15:04"),
		"price_mode":      "free", "category_slug": "concerts",
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
