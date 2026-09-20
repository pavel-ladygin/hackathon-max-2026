package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/auth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

func TestCreateRoomPersistsCompleteSafeRoomAndInvite(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	svc := newCreateService(t, db, behavior.Recorder{})
	ctx := context.Background()
	request := api.CreateRoomRequest{CityId: f.city, Name: "  Комната на выходные  "}
	response, err := svc.Create(ctx, contracts.Principal{UserID: f.creator}, "create-complete", request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Room.Id == uuid.Nil || response.Room.Name != "Комната на выходные" || response.Room.CityId != f.city || response.Room.State != "collecting_intents" || response.Room.RoundNo != 1 || response.Room.Version != 1 {
		t.Fatalf("unexpected initial room snapshot: %+v", response.Room)
	}
	if len(response.Room.Participants) != 1 || response.Room.Participants[0].Id != f.creator || response.Room.Participants[0].DisplayName == "" || response.Room.Participants[0].Role != "creator" || response.Room.Participants[0].IntentReady {
		t.Fatalf("unsafe or incomplete initial participants: %+v", response.Room.Participants)
	}
	for name, value := range map[string]any{"match": response.Room.Match, "my_intent": response.Room.MyIntent, "pool": response.Room.Pool} {
		if !nullableIsNull(value) {
			t.Errorf("room.%s = %#v; want explicit null", name, value)
		}
	}
	if response.Invite.Token == "" || response.Invite.Url == "" || response.Invite.MaxDeepLink == "" || !strings.Contains(response.Invite.Url, response.Invite.Token) || !strings.Contains(response.Invite.MaxDeepLink, response.Invite.Token) {
		t.Fatalf("invite is incomplete: %+v", response.Invite)
	}
	if nullableIsNull(response.Room.Invite) {
		t.Fatal("creator snapshot invite is null")
	}
	roomInvite, err := response.Room.Invite.Get()
	if err != nil || roomInvite.Url != response.Invite.Url || roomInvite.MaxDeepLink != response.Invite.MaxDeepLink || !roomInvite.ExpiresAt.Equal(response.Invite.ExpiresAt) {
		t.Fatalf("creator snapshot invite = %+v, %v; want create invite %+v", roomInvite, err, response.Invite)
	}
	if response.Room.ExpiresAt.Sub(response.Room.CreatedAt) < 47*time.Hour || response.Room.ExpiresAt.Sub(response.Room.CreatedAt) > 49*time.Hour || response.Invite.ExpiresAt.Sub(response.Room.CreatedAt) < 47*time.Hour {
		t.Fatalf("room/invite TTL must be 48h: room=%s created=%s invite=%s", response.Room.ExpiresAt, response.Room.CreatedAt, response.Invite.ExpiresAt)
	}
	var tokenHash, ciphertext []byte
	if err := db.QueryRow(ctx, "SELECT token_hash, token_ciphertext FROM room_invites WHERE room_id=$1", response.Room.Id).Scan(&tokenHash, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if string(tokenHash) == response.Invite.Token || string(ciphertext) == response.Invite.Token || strings.Contains(string(ciphertext), response.Invite.Token) || len(tokenHash) != sha256.Size {
		t.Fatal("invite token was persisted in plaintext")
	}
	assertCreateRows(t, db, f.creator, response.Room.Id, 1, 1, 1)
	assertNoPrivateRoomFields(t, response)
}

func TestCreateRoomValidationAndActiveRoomPolicy(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	svc := newCreateService(t, db, behavior.Recorder{})
	ctx := context.Background()
	principal := contracts.Principal{UserID: f.creator}
	for name, request := range map[string]api.CreateRoomRequest{
		"required name":    {CityId: f.city},
		"required city":    {Name: "room"},
		"missing city":     {CityId: uuid.New(), Name: "room"},
		"too long unicode": {CityId: f.city, Name: strings.Repeat("я", 81)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Create(ctx, principal, "invalid-"+name, request)
			if !errors.Is(err, rooms.ErrValidation) {
				t.Fatalf("error = %v; want validation", err)
			}
		})
	}
	if _, err := svc.Create(ctx, contracts.Principal{}, "anonymous", api.CreateRoomRequest{CityId: f.city, Name: "room"}); !errors.Is(err, rooms.ErrUnauthenticated) {
		t.Fatalf("anonymous error = %v; want unauthenticated", err)
	}
	if _, err := svc.Create(ctx, principal, "first-key", api.CreateRoomRequest{CityId: f.city, Name: "room"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, principal, "second-key", api.CreateRoomRequest{CityId: f.city, Name: "another"}); !errors.Is(err, rooms.ErrActiveRoomExists) {
		t.Fatalf("active room error = %v; want ACTIVE_ROOM_EXISTS", err)
	}
}

func TestCreateRoomIdempotencyAndConcurrency(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	request := api.CreateRoomRequest{CityId: f.city, Name: "Replay"}
	first := newCreateService(t, db, behavior.Recorder{})
	got, err := first.Create(context.Background(), contracts.Principal{UserID: f.creator}, "replay-key", request)
	if err != nil {
		t.Fatal(err)
	}
	second := newCreateService(t, db, behavior.Recorder{})
	replayed, err := second.Create(context.Background(), contracts.Principal{UserID: f.creator}, "replay-key", request)
	if err != nil {
		t.Fatal(err)
	}
	assertIdenticalJSON(t, got, replayed)
	if _, err := second.Create(context.Background(), contracts.Principal{UserID: f.creator}, "replay-key", api.CreateRoomRequest{CityId: f.city, Name: "changed"}); !errors.Is(err, rooms.ErrIdempotencyConflict) {
		t.Fatalf("different payload = %v; want idempotency conflict", err)
	}
	var stored string
	if err := db.QueryRow(context.Background(), "SELECT response_body::text FROM idempotency_records WHERE user_id=$1 AND key='replay-key' AND route='/api/v1/rooms'", f.creator).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, got.Invite.Token) || !strings.Contains(stored, "ciphertext") {
		t.Fatalf("idempotency response must be encrypted envelope, got %s", stored)
	}

	concurrent := f.newUser(t)
	concurrentService := newCreateService(t, db, behavior.Recorder{})
	start := make(chan struct{})
	results := make(chan createResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			r, err := concurrentService.Create(context.Background(), contracts.Principal{UserID: concurrent}, "same-key", request)
			results <- createResult{r, err}
		}()
	}
	close(start)
	var success []api.CreateRoomResponse
	for range 2 {
		result := <-results
		if result.err == nil {
			success = append(success, result.response)
		} else {
			t.Errorf("same-key create: %v", result.err)
		}
	}
	if len(success) != 2 {
		t.Fatalf("same-key successful responses=%d", len(success))
	}
	assertIdenticalJSON(t, success[0], success[1])
	assertCreateRows(t, db, concurrent, success[0].Room.Id, 1, 1, 1)

	different := f.newUser(t)
	differentService := newCreateService(t, db, behavior.Recorder{})
	start = make(chan struct{})
	results = make(chan createResult, 2)
	for _, key := range []string{"one-create", "two-create"} {
		go func(key string) {
			<-start
			r, err := differentService.Create(context.Background(), contracts.Principal{UserID: different}, key, request)
			results <- createResult{r, err}
		}(key)
	}
	close(start)
	var successes, active int
	for range 2 {
		result := <-results
		if result.err == nil {
			successes++
		} else if errors.Is(result.err, rooms.ErrActiveRoomExists) {
			active++
		} else {
			t.Errorf("different-key create: %v", result.err)
		}
	}
	if successes != 1 || active != 1 {
		t.Fatalf("different keys successes=%d active=%d", successes, active)
	}
}

func TestCreateRoomRetiresOnlyRestartableExhaustedRoomAndRollsBackOnRecorderFailure(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	old := f.insertRoom(t, f.creator)
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='exhausted', round_no=2 WHERE id=$1", old); err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct {
		user uuid.UUID
		role string
	}{{f.creator, "creator"}, {f.member, "participant"}} {
		if _, err := db.Exec(ctx, "INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,$3)", old, member.user, member.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, "INSERT INTO room_intents(room_id,user_id,round_no,date_options,day_types,time_slots,category_slugs,budget_max_minor,location_lat,location_lng,radius_m,exclusion_slugs) VALUES($1,$2,2,'{}','{}','{}','{}',0,55,37,100,'{}')", old, f.creator); err != nil {
		t.Fatal(err)
	}
	response, err := newCreateService(t, db, behavior.Recorder{}).Create(ctx, contracts.Principal{UserID: f.creator}, "replace-key", api.CreateRoomRequest{CityId: f.city, Name: "replacement"})
	if err != nil {
		t.Fatal(err)
	}
	var active bool
	var lat *float64
	if err := db.QueryRow(ctx, "SELECT is_active FROM room_members WHERE room_id=$1 AND user_id=$2", old, f.creator).Scan(&active); err != nil || active {
		t.Fatalf("old creator active=%v err=%v", active, err)
	}
	if err := db.QueryRow(ctx, "SELECT location_lat FROM room_intents WHERE room_id=$1 AND user_id=$2", old, f.creator).Scan(&lat); err != nil || lat != nil {
		t.Fatalf("old coordinates=%v err=%v", lat, err)
	}
	assertCreateRows(t, db, f.creator, response.Room.Id, 1, 1, 1)

	failingUser := f.newUser(t)
	failingOld := f.insertRoom(t, failingUser)
	if _, err := db.Exec(ctx, "UPDATE rooms SET state='exhausted', round_no=2 WHERE id=$1", failingOld); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'creator')", failingOld, failingUser); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO room_intents(room_id,user_id,round_no,date_options,day_types,time_slots,category_slugs,budget_max_minor,location_lat,location_lng,radius_m,exclusion_slugs) VALUES($1,$2,2,'{}','{}','{}','{}',0,55,37,100,'{}')", failingOld, failingUser); err != nil {
		t.Fatal(err)
	}
	_, err = newCreateService(t, db, failingRecorder{err: errors.New("behavior unavailable")}).Create(ctx, contracts.Principal{UserID: failingUser}, "rollback-key", api.CreateRoomRequest{CityId: f.city, Name: "rollback"})
	if err == nil {
		t.Fatal("recorder failure unexpectedly committed")
	}
	for _, query := range []string{"SELECT count(*) FROM rooms WHERE creator_user_id=$1 AND name='rollback'", "SELECT count(*) FROM idempotency_records WHERE user_id=$1 AND key='rollback-key'", "SELECT count(*) FROM behavior_events WHERE user_id=$1 AND type='room_create'"} {
		var count int
		if err := db.QueryRow(ctx, query, failingUser).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback query %q = %d, %v", query, count, err)
		}
	}
	if err := db.QueryRow(ctx, "SELECT is_active FROM room_members WHERE room_id=$1 AND user_id=$2", failingOld, failingUser).Scan(&active); err != nil || !active {
		t.Fatalf("failed replacement retired old membership: active=%v err=%v", active, err)
	}
	if err := db.QueryRow(ctx, "SELECT location_lat FROM room_intents WHERE room_id=$1 AND user_id=$2", failingOld, failingUser).Scan(&lat); err != nil || lat == nil {
		t.Fatalf("failed replacement cleared old coordinates: lat=%v err=%v", lat, err)
	}
}

func TestCreateRoomOnlyBlocksLiveActiveStates(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	for _, state := range []string{"collecting_intents", "ranking", "voting", "matched", "exhausted"} {
		t.Run(state, func(t *testing.T) {
			user := f.newUser(t)
			old := f.insertRoom(t, user)
			round := 1
			if state == "exhausted" {
				round = 3
			}
			if _, err := db.Exec(ctx, "UPDATE rooms SET state=$2, round_no=$3 WHERE id=$1", old, state, round); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'creator')", old, user); err != nil {
				t.Fatal(err)
			}
			_, err := newCreateService(t, db, behavior.Recorder{}).Create(ctx, contracts.Principal{UserID: user}, "state-key-"+state, api.CreateRoomRequest{CityId: f.city, Name: "next"})
			if state == "collecting_intents" || state == "ranking" || state == "voting" {
				if !errors.Is(err, rooms.ErrActiveRoomExists) {
					t.Fatalf("error=%v; want active room", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("terminal state should not block create: %v", err)
			}
		})
	}
	user := f.newUser(t)
	old := f.insertRoom(t, user)
	if _, err := db.Exec(ctx, "UPDATE rooms SET expires_at=now()-interval '1 second' WHERE id=$1", old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'creator')", old, user); err != nil {
		t.Fatal(err)
	}
	if _, err := newCreateService(t, db, behavior.Recorder{}).Create(ctx, contracts.Principal{UserID: user}, "expired-key", api.CreateRoomRequest{CityId: f.city, Name: "next"}); err != nil {
		t.Fatalf("expired state should not block create: %v", err)
	}
}

func TestCreateRoomHTTPUsesAuthAndSafeErrors(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	svc := newCreateService(t, db, behavior.Recorder{})
	authService, err := auth.NewService(db, "test-secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(authService.Middleware)
	svc.RegisterRoutes(r)
	token := strings.Repeat("A", 43)
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(context.Background(), "INSERT INTO auth_sessions(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')", uuid.New(), f.creator, hash[:]); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"city_id":%q,"name":"HTTP room"}`, f.city)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "http-create")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("create response containing invite must not be cacheable")
	}
	for name, tc := range map[string]struct {
		body string
		keys []string
	}{
		"missing key":     {body, nil},
		"duplicate key":   {body, []string{"valid-key", "another-key"}},
		"short key":       {body, []string{"short"}},
		"long key":        {body, []string{strings.Repeat("x", 129)}},
		"null body":       {"null", []string{"valid-key"}},
		"missing name":    {fmt.Sprintf(`{"city_id":%q}`, f.city), []string{"valid-key"}},
		"null name":       {fmt.Sprintf(`{"city_id":%q,"name":null}`, f.city), []string{"valid-key"}},
		"unknown field":   {fmt.Sprintf(`{"city_id":%q,"name":"ok","user_id":%q}`, f.city, f.member), []string{"valid-key"}},
		"trailing object": {body + `{}`, []string{"valid-key"}},
		"zero byte":       {fmt.Sprintf(`{"city_id":%q,"name":"bad\u0000name"}`, f.city), []string{"valid-key"}},
		"oversized body":  {strings.Repeat(" ", 17*1024) + body, []string{"valid-key"}},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			for _, key := range tc.keys {
				req.Header.Add("Idempotency-Key", key)
			}
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"VALIDATION_FAILED"`) {
				t.Fatalf("status=%d body=%s; want safe validation error", res.Code, res.Body.String())
			}
		})
	}
	unauthenticated := httptest.NewRecorder()
	r.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(body)))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d", unauthenticated.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(`{"city_id":"bad","name":"x"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "http-invalid")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || strings.Contains(strings.ToLower(res.Body.String()), "sql") || strings.Contains(strings.ToLower(res.Body.String()), "token") {
		t.Fatalf("unsafe HTTP error: status=%d body=%s", res.Code, res.Body.String())
	}
}

type createResult struct {
	response api.CreateRoomResponse
	err      error
}
type failingRecorder struct{ err error }

func (f failingRecorder) Record(context.Context, store.DBTX, contracts.ServerBehaviorEvent) error {
	return f.err
}

func newCreateService(t *testing.T, db *store.Pool, recorder contracts.BehaviorRecorder) *rooms.Service {
	t.Helper()
	invites, err := rooms.NewInviteCodec([]byte("0123456789abcdef0123456789abcdef"), 1, "https://app.test/invite/{token}", "https://max.test/bot?startapp={token}")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := rooms.NewCreateService(db, recorder, invites)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func assertCreateRows(t *testing.T, db *store.Pool, user, room uuid.UUID, members, invites, behavior int) {
	t.Helper()
	ctx := context.Background()
	for query, want := range map[string]int{"SELECT count(*) FROM room_members WHERE room_id=$1": members, "SELECT count(*) FROM room_invites WHERE room_id=$1": invites, "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND user_id=$2 AND type='room_create'": behavior} {
		var got int
		var err error
		if strings.Contains(query, "$2") {
			err = db.QueryRow(ctx, query, room, user).Scan(&got)
		} else {
			err = db.QueryRow(ctx, query, room).Scan(&got)
		}
		if err != nil || got != want {
			t.Fatalf("%s = %d, %v; want %d", query, got, err, want)
		}
	}
}
func nullableIsNull(v any) bool {
	method := reflect.ValueOf(v).MethodByName("IsNull")
	return method.IsValid() && method.Call(nil)[0].Bool()
}
func assertNoPrivateRoomFields(t *testing.T, response api.CreateRoomResponse) {
	t.Helper()
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"max_user_id", "token_ciphertext", "token_hash", "location_lat", "location_lng", "behavior"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("response leaks %s: %s", forbidden, raw)
		}
	}
}
func assertIdenticalJSON(t *testing.T, left, right any) {
	t.Helper()
	a, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("JSON response replay differs:\n%s\n%s", a, b)
	}
}
