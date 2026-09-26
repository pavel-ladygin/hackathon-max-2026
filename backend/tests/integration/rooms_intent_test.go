package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
)

func validIntentRequest() api.RoomIntentRequest {
	return api.RoomIntentRequest{
		Dates:          []openapi_types.Date{{Time: time.Now().UTC().AddDate(0, 0, 1)}},
		DayTypes:       []api.DayType{api.Weekend},
		TimeSlots:      []api.TimeSlot{api.Evening},
		CategorySlugs:  []api.CategorySlug{api.Concerts},
		BudgetMaxMinor: 250000,
		Location:       nullable.NewNullableWithValue(api.GeoPoint{Lat: 55.75, Lng: 37.62}),
		RadiusM:        nullable.NewNullableWithValue(1500),
		ExclusionSlugs: []api.RoomIntentRequestExclusionSlugs{api.RoomIntentRequestExclusionSlugsVeryLoud},
		FreeText:       nullable.NewNullableWithValue("private preference"),
	}
}

func setupIntentRoom(t *testing.T, recorder contracts.BehaviorRecorder) (*roomFixture, *rooms.Service) {
	t.Helper()
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := db.Exec(context.Background(), "INSERT INTO room_member_round_state(room_id,user_id,round_no) VALUES($1,$2,1)", f.room, user); err != nil {
			t.Fatal(err)
		}
	}
	// Intent transition now always persists a B6 pool. Keep this B5 fixture
	// deterministic with one valid candidate so the assertions below exercise
	// ranking/privacy semantics instead of the zero-candidate exhausted path.
	event := f.newEvent(t)
	builder := &poolBuilderFake{result: contracts.BuildResult{
		RankerVersion:    "intent-test",
		InputFingerprint: "intent-test",
		Candidates:       []contracts.Candidate{{EventID: event}},
	}}
	invites, err := rooms.NewInviteCodec([]byte("0123456789abcdef0123456789abcdef"), 1, "https://app.test/invite/{token}", "https://max.test/bot?startapp={token}")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := rooms.NewCreateService(db, recorder, invites, builder)
	if err != nil {
		t.Fatal(err)
	}
	return f, svc
}

func TestReplaceIntentValidation(t *testing.T) {
	f, svc := setupIntentRoom(t, behavior.Recorder{})
	base := validIntentRequest()
	cases := map[string]func(*api.RoomIntentRequest){
		"no dates":           func(r *api.RoomIntentRequest) { r.Dates = nil },
		"too many dates":     func(r *api.RoomIntentRequest) { r.Dates = make([]openapi_types.Date, 15) },
		"duplicate dates":    func(r *api.RoomIntentRequest) { r.Dates = append(r.Dates, r.Dates[0]) },
		"past date":          func(r *api.RoomIntentRequest) { r.Dates[0].Time = time.Now().UTC().AddDate(0, 0, -1) },
		"nil day types":      func(r *api.RoomIntentRequest) { r.DayTypes = nil },
		"unknown day":        func(r *api.RoomIntentRequest) { r.DayTypes = []api.DayType{"holiday"} },
		"duplicate day":      func(r *api.RoomIntentRequest) { r.DayTypes = []api.DayType{api.Weekend, api.Weekend} },
		"nil time slots":     func(r *api.RoomIntentRequest) { r.TimeSlots = nil },
		"unknown slot":       func(r *api.RoomIntentRequest) { r.TimeSlots = []api.TimeSlot{"late"} },
		"no categories":      func(r *api.RoomIntentRequest) { r.CategorySlugs = nil },
		"unknown category":   func(r *api.RoomIntentRequest) { r.CategorySlugs = []api.CategorySlug{"secret"} },
		"duplicate category": func(r *api.RoomIntentRequest) { r.CategorySlugs = []api.CategorySlug{api.Concerts, api.Concerts} },
		"negative budget":    func(r *api.RoomIntentRequest) { r.BudgetMaxMinor = -1 },
		"budget overflow":    func(r *api.RoomIntentRequest) { r.BudgetMaxMinor = 100000001 },
		"latitude": func(r *api.RoomIntentRequest) {
			r.Location = nullable.NewNullableWithValue(api.GeoPoint{Lat: 91, Lng: 0})
		},
		"longitude": func(r *api.RoomIntentRequest) {
			r.Location = nullable.NewNullableWithValue(api.GeoPoint{Lat: 0, Lng: 181})
		},
		"radius too small":  func(r *api.RoomIntentRequest) { r.RadiusM = nullable.NewNullableWithValue(99) },
		"radius too large":  func(r *api.RoomIntentRequest) { r.RadiusM = nullable.NewNullableWithValue(50001) },
		"nil exclusions":    func(r *api.RoomIntentRequest) { r.ExclusionSlugs = nil },
		"unknown exclusion": func(r *api.RoomIntentRequest) { r.ExclusionSlugs = []api.RoomIntentRequestExclusionSlugs{"unknown"} },
		"duplicate exclusion": func(r *api.RoomIntentRequest) {
			r.ExclusionSlugs = []api.RoomIntentRequestExclusionSlugs{api.RoomIntentRequestExclusionSlugsVeryLoud, api.RoomIntentRequestExclusionSlugsVeryLoud}
		},
		"free text over 300":     func(r *api.RoomIntentRequest) { r.FreeText = nullable.NewNullableWithValue(strings.Repeat("я", 301)) },
		"free text contains nul": func(r *api.RoomIntentRequest) { r.FreeText = nullable.NewNullableWithValue("a\x00b") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := base
			request.Dates = append([]openapi_types.Date(nil), base.Dates...)
			mutate(&request)
			_, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, request)
			if !errors.Is(err, rooms.ErrValidation) && !errors.Is(err, rooms.ErrPastIntentDate) {
				t.Fatalf("error=%v; want validation", err)
			}
		})
	}
}

func TestReplaceIntentUpsertPrivacyTransitionAndBehavior(t *testing.T) {
	f, svc := setupIntentRoom(t, behavior.Recorder{})
	ctx := context.Background()
	first := validIntentRequest()
	snapshot, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: f.creator}, f.room, first)
	if err != nil || transitioned || snapshot.State != api.RoomStateCollectingIntents {
		t.Fatalf("first submit snapshot=%+v transitioned=%v err=%v", snapshot, transitioned, err)
	}
	stored, err := snapshot.MyIntent.Get()
	if err != nil || stored.Version != 1 || len(snapshot.Participants) != 2 || !snapshot.Participants[0].IntentReady {
		t.Fatalf("first intent/snapshot=%+v err=%v", snapshot, err)
	}
	replacement := validIntentRequest()
	replacement.BudgetMaxMinor = 777
	replacement.FreeText = nullable.NewNullableWithValue("replacement-private")
	snapshot, transitioned, err = svc.ReplaceIntent(ctx, contracts.Principal{UserID: f.creator}, f.room, replacement)
	stored, getErr := snapshot.MyIntent.Get()
	if err != nil || getErr != nil || transitioned || stored.Version != 2 || stored.BudgetMaxMinor != 777 {
		t.Fatalf("replacement=%+v transitioned=%v err=%v/%v", stored, transitioned, err, getErr)
	}
	peer, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: f.member}, f.room, first)
	if err != nil || !transitioned || peer.State != api.RoomStateVoting || peer.Version != 3 {
		t.Fatalf("second submit snapshot=%+v transitioned=%v err=%v", peer, transitioned, err)
	}
	raw, _ := json.Marshal(peer)
	if strings.Contains(string(raw), "replacement-private") || strings.Contains(string(raw), `"budget_max_minor":777`) {
		t.Fatalf("peer snapshot leaked creator intent: %s", raw)
	}
	var events int
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type='room_intent_submitted'", f.room).Scan(&events); err != nil || events != 3 {
		t.Fatalf("intent behavior count=%d err=%v; want 3", events, err)
	}
	for _, user := range []uuid.UUID{f.creator, f.member} {
		var ready bool
		if err := f.db.QueryRow(ctx, "SELECT ready FROM room_member_round_state WHERE room_id=$1 AND user_id=$2 AND round_no=1", f.room, user).Scan(&ready); err != nil || !ready {
			t.Fatalf("user %s ready=%v err=%v", user, ready, err)
		}
	}
}

func TestReplaceIntentStateAndAccessErrors(t *testing.T) {
	for name, state := range map[string]string{"ranking": "ranking", "voting": "voting", "matched": "matched", "round limit": "exhausted"} {
		t.Run(name, func(t *testing.T) {
			f, svc := setupIntentRoom(t, behavior.Recorder{})
			round := 1
			if state == "exhausted" {
				round = 3
			}
			if _, err := f.db.Exec(context.Background(), "UPDATE rooms SET state=$2, round_no=$3 WHERE id=$1", f.room, state, round); err != nil {
				t.Fatal(err)
			}
			_, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, validIntentRequest())
			want := rooms.ErrIntentLocked
			if state == "matched" {
				want = rooms.ErrAlreadyMatched
			}
			if state == "exhausted" {
				want = rooms.ErrRoundLimitReached
			}
			if !errors.Is(err, want) {
				t.Fatalf("error=%v; want %v", err, want)
			}
		})
	}
	f, svc := setupIntentRoom(t, behavior.Recorder{})
	for name, principal := range map[string]contracts.Principal{"anonymous": {}, "outsider": {UserID: f.third}} {
		t.Run(name, func(t *testing.T) {
			_, _, err := svc.ReplaceIntent(context.Background(), principal, f.room, validIntentRequest())
			want := rooms.ErrRoomNotFound
			if name == "anonymous" {
				want = rooms.ErrUnauthenticated
			}
			if !errors.Is(err, want) {
				t.Fatalf("error=%v; want %v", err, want)
			}
		})
	}
}

func TestReplaceIntentRecorderFailureRollsBack(t *testing.T) {
	f, svc := setupIntentRoom(t, failingRecorder{err: errors.New("recorder unavailable")})
	_, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, validIntentRequest())
	if err == nil {
		t.Fatal("recorder failure unexpectedly committed")
	}
	for _, query := range []string{
		"SELECT count(*) FROM room_intents WHERE room_id=$1",
		"SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type='room_intent_submitted'",
		"SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND ready",
	} {
		var count int
		if scanErr := f.db.QueryRow(context.Background(), query, f.room).Scan(&count); scanErr != nil || count != 0 {
			t.Fatalf("rollback query %q count=%d err=%v", query, count, scanErr)
		}
	}
}

func TestReplaceIntentConcurrentParticipants(t *testing.T) {
	f, svc := setupIntentRoom(t, behavior.Recorder{})
	start := make(chan struct{})
	var wg sync.WaitGroup
	type result struct {
		transitioned bool
		err          error
	}
	results := make(chan result, 2)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		wg.Add(1)
		go func(user uuid.UUID) {
			defer wg.Done()
			<-start
			_, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: user}, f.room, validIntentRequest())
			results <- result{transitioned: transitioned, err: err}
		}(user)
	}
	close(start)
	wg.Wait()
	close(results)
	transitioned := false
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		transitioned = transitioned || result.transitioned
	}
	if !transitioned {
		t.Fatal("concurrent submissions left both participants ready without building the pool")
	}
	var state string
	var version, pools, intents, ready int
	if err := f.db.QueryRow(context.Background(), "SELECT state,version FROM rooms WHERE id=$1", f.room).Scan(&state, &version); err != nil || state != "voting" || version != 3 {
		t.Fatalf("room state=%s version=%d err=%v", state, version, err)
	}
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM room_pools WHERE room_id=$1", f.room).Scan(&pools); err != nil || pools != 1 {
		t.Fatalf("pool rows=%d err=%v; want one", pools, err)
	}
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM room_intents WHERE room_id=$1", f.room).Scan(&intents); err != nil || intents != 2 {
		t.Fatalf("intent rows=%d err=%v; want two", intents, err)
	}
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND ready", f.room).Scan(&ready); err != nil || ready != 2 {
		t.Fatalf("ready states=%d err=%v; want two", ready, err)
	}
}

func TestReplaceIntentConcurrentSameUserSerializesVersions(t *testing.T) {
	f, svc := setupIntentRoom(t, behavior.Recorder{})
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, budget := range []int{111, 222} {
		go func(budget int) {
			<-start
			request := validIntentRequest()
			request.BudgetMaxMinor = budget
			_, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, request)
			results <- err
		}(budget)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var version, rows int
	var budget int
	if err := f.db.QueryRow(context.Background(), "SELECT version,budget_max_minor FROM room_intents WHERE room_id=$1 AND user_id=$2 AND round_no=1", f.room, f.creator).Scan(&version, &budget); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM room_intents WHERE room_id=$1 AND user_id=$2 AND round_no=1", f.room, f.creator).Scan(&rows); err != nil || version != 2 || rows != 1 || (budget != 111 && budget != 222) {
		t.Fatalf("version=%d rows=%d budget=%d err=%v", version, rows, budget, err)
	}
}

func TestReplaceIntentTerminalAndHiddenRoomDoNotMutate(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*testing.T, *roomFixture)
		user   func(*roomFixture) uuid.UUID
		wanted error
	}{
		{"matched historical member", func(t *testing.T, f *roomFixture) {
			_, _ = f.db.Exec(context.Background(), "UPDATE rooms SET state='matched' WHERE id=$1", f.room)
			_, _ = f.db.Exec(context.Background(), "UPDATE room_members SET is_active=false WHERE room_id=$1", f.room)
		}, func(f *roomFixture) uuid.UUID { return f.member }, rooms.ErrAlreadyMatched},
		{"inactive collecting member", func(t *testing.T, f *roomFixture) {
			_, _ = f.db.Exec(context.Background(), "UPDATE room_members SET is_active=false WHERE room_id=$1 AND user_id=$2", f.room, f.member)
		}, func(f *roomFixture) uuid.UUID { return f.member }, rooms.ErrRoomNotFound},
		{"expired", func(t *testing.T, f *roomFixture) {
			_, _ = f.db.Exec(context.Background(), "UPDATE rooms SET expires_at=now()-interval '1 second' WHERE id=$1", f.room)
		}, func(f *roomFixture) uuid.UUID { return f.creator }, rooms.ErrRoomNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, svc := setupIntentRoom(t, behavior.Recorder{})
			tc.setup(t, f)
			_, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: tc.user(f)}, f.room, validIntentRequest())
			if !errors.Is(err, tc.wanted) {
				t.Fatalf("error=%v; want %v", err, tc.wanted)
			}
			var count int
			if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM room_intents WHERE room_id=$1", f.room).Scan(&count); err != nil || count != 0 {
				t.Fatalf("terminal request mutated intents: count=%d err=%v", count, err)
			}
		})
	}
}

func TestReplaceIntentHTTPStrictDecodeAndTransitionResponse(t *testing.T) {
	f, svc := setupIntentRoom(t, behavior.Recorder{})
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: f.creator})))
		})
	})
	svc.RegisterRoutes(router)
	valid, _ := json.Marshal(validIntentRequest())
	validJSON := string(valid)
	locationJSON := `"location":{"lat":55.75,"lng":37.62}`
	cases := map[string]string{
		"empty": "", "null": "null", "unknown": strings.TrimSuffix(validJSON, "}") + `,"user_id":"x"}`,
		"trailing": validJSON + `{}`, "missing": `{"dates":[]}`, "required null": strings.Replace(validJSON, `"dates":[`, `"dates":null,"ignored":[`, 1),
		"location missing lat": strings.Replace(validJSON, locationJSON, `"location":{"lng":37.62}`, 1),
		"location missing lng": strings.Replace(validJSON, locationJSON, `"location":{"lat":55.75}`, 1),
		"location null lat":    strings.Replace(validJSON, locationJSON, `"location":{"lat":null,"lng":37.62}`, 1),
		"location null lng":    strings.Replace(validJSON, locationJSON, `"location":{"lat":55.75,"lng":null}`, 1),
		"oversize":             strings.Repeat(" ", 17*1024) + validJSON,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/rooms/%s/intent/me", f.room), strings.NewReader(body)))
			if res.Code != http.StatusBadRequest || res.Header().Get("Cache-Control") != "no-store" || !strings.Contains(res.Body.String(), `"code":"VALIDATION_FAILED"`) {
				t.Fatalf("status=%d headers=%v body=%s", res.Code, res.Header(), res.Body.String())
			}
		})
	}
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.member}, f.room, validIntentRequest()); err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/rooms/%s/intent/me", f.room), strings.NewReader(validJSON)))
	if res.Code != http.StatusOK || res.Header().Get("Retry-After") != "" || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("transition response status=%d headers=%v body=%s", res.Code, res.Header(), res.Body.String())
	}
}
