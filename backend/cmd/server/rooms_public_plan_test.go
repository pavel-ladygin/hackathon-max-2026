package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHTTPPublicPlanCreateReplayAndInviteBootstrapPreview(t *testing.T) {
	db := openServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cityID := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'UTC',55.75,37.61)`, cityID, "public-plan-http-"+cityID.String()); err != nil {
		t.Fatal(err)
	}
	var userIDs []uuid.UUID
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, statement := range []string{
			`DELETE FROM behavior_events WHERE user_id=ANY($1)`,
			`DELETE FROM idempotency_records WHERE user_id=ANY($1)`,
			`DELETE FROM room_matches WHERE room_id IN (SELECT id FROM rooms WHERE creator_user_id=ANY($1))`,
			`DELETE FROM rooms WHERE creator_user_id=ANY($1)`,
			`DELETE FROM auth_sessions WHERE user_id=ANY($1)`,
			`DELETE FROM users WHERE id=ANY($1)`,
		} {
			if _, err := db.Exec(cleanupCtx, statement, userIDs); err != nil {
				t.Errorf("cleanup %q: %v", statement, err)
			}
		}
		if _, err := db.Exec(cleanupCtx, `DELETE FROM cities WHERE id=$1`, cityID); err != nil {
			t.Errorf("cleanup city: %v", err)
		}
	})

	h, err := newHandler(context.Background(), serverTestConfig(), db, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, target, token string, body string, idempotencyKey string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res
	}
	bootstrap := func(maxID int64, name, startParam string) (bootstrapHTTPResponse, []byte) {
		t.Helper()
		initData := signedHTTPAcceptanceMAXInitData(t, maxID, name, startParam)
		payloadObject := map[string]string{"init_data": initData}
		if startParam != "" {
			payloadObject["start_param"] = startParam
		}
		payload, _ := json.Marshal(payloadObject)
		res := request(http.MethodPost, "/api/v1/auth/max/bootstrap", "", string(payload), "")
		if res.Code != http.StatusOK {
			t.Fatalf("bootstrap %s status=%d body=%s", name, res.Code, res.Body.String())
		}
		var decoded bootstrapHTTPResponse
		if err := json.Unmarshal(res.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.AccessToken == "" || decoded.User.ID == uuid.Nil {
			t.Fatalf("bootstrap %s returned incomplete response: %s", name, res.Body.String())
		}
		userIDs = append(userIDs, decoded.User.ID)
		return decoded, append([]byte(nil), res.Body.Bytes()...)
	}

	maxBase := time.Now().UnixNano()
	creator, _ := bootstrap(maxBase, "Public plan creator", "")
	date := time.Now().UTC().AddDate(0, 0, 3).Format(time.DateOnly)
	firstBody := fmt.Sprintf(`{"city_id":%q,"name":"Public proposal","public_plan":{"dates":[%q],"time_slots":["evening"],"category_slugs":["concerts"],"budget_max_minor":250000}}`, cityID.String(), date)
	created := request(http.MethodPost, "/api/v1/rooms", creator.AccessToken, firstBody, "http-public-plan-create")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var createdResponse createRoomHTTPResponse
	if err := json.Unmarshal(created.Body.Bytes(), &createdResponse); err != nil {
		t.Fatal(err)
	}
	if createdResponse.Invite.Token == "" || createdResponse.Room.ID == uuid.Nil {
		t.Fatalf("create response is incomplete: %s", created.Body.String())
	}

	// Property order inside public_plan is immaterial to the request hash.
	reorderedBody := fmt.Sprintf(`{"public_plan":{"budget_max_minor":250000,"category_slugs":["concerts"],"time_slots":["evening"],"dates":[%q]},"name":"Public proposal","city_id":%q}`, date, cityID.String())
	replayed := request(http.MethodPost, "/api/v1/rooms", creator.AccessToken, reorderedBody, "http-public-plan-create")
	if replayed.Code != http.StatusCreated || replayed.Body.String() != created.Body.String() {
		t.Fatalf("reordered equivalent replay status/body=%d/%s; want byte-identical original %s", replayed.Code, replayed.Body.String(), created.Body.String())
	}
	differentPlanBody := fmt.Sprintf(`{"city_id":%q,"name":"Public proposal","public_plan":{"dates":[%q],"time_slots":["evening"],"category_slugs":["concerts"],"budget_max_minor":250001}}`, cityID.String(), date)
	conflict := request(http.MethodPost, "/api/v1/rooms", creator.AccessToken, differentPlanBody, "http-public-plan-create")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("same key with different plan status/body=%d/%s; want conflict", conflict.Code, conflict.Body.String())
	}

	joiner, joinerRaw := bootstrap(maxBase+1, "Public plan previewer", createdResponse.Invite.Token)
	if joiner.InviteContext == nil || joiner.InviteContext.Status != "joinable" {
		t.Fatalf("invite bootstrap context=%+v; want joinable context", joiner.InviteContext)
	}
	var preview map[string]json.RawMessage
	var bootstrapResponse map[string]json.RawMessage
	if err := json.Unmarshal(joinerRaw, &bootstrapResponse); err != nil {
		t.Fatal(err)
	}
	previewRaw := bootstrapResponse["invite_context"]
	if len(previewRaw) == 0 || string(previewRaw) == "null" {
		t.Fatalf("bootstrap invite context missing: %s", joinerRaw)
	}
	if err := json.Unmarshal(previewRaw, &preview); err != nil {
		t.Fatal(err)
	}
	if _, ok := preview["public_plan"]; !ok {
		t.Fatalf("bootstrap invite preview omits public_plan: %s", previewRaw)
	}
	for _, forbidden := range []string{"my_intent", "location_lat", "location_lng"} {
		if _, ok := preview[forbidden]; ok {
			t.Errorf("bootstrap invite preview exposes %q: %s", forbidden, previewRaw)
		}
	}
	var memberCount int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM room_members WHERE room_id=$1`, createdResponse.Room.ID).Scan(&memberCount); err != nil || memberCount != 1 {
		t.Fatalf("bootstrap preview members=%d err=%v; want creator only", memberCount, err)
	}
}
