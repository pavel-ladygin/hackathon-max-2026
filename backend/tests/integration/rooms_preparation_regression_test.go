package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

type preparationFacts struct {
	state            string
	round, version   int
	nextReady, pools int
	votes            int
}

func preparationFactsFor(t *testing.T, f *roomFixture) preparationFacts {
	t.Helper()
	ctx := context.Background()
	var got preparationFacts
	if err := f.db.QueryRow(ctx, "SELECT state,round_no,version FROM rooms WHERE id=$1", f.room).Scan(&got.state, &got.round, &got.version); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM room_member_round_state WHERE room_id=$1 AND round_no=$2 AND ready", f.room, got.round+1).Scan(&got.nextReady); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM room_pools WHERE room_id=$1", f.room).Scan(&got.pools); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(ctx, "SELECT count(*) FROM room_votes WHERE room_id=$1", f.room).Scan(&got.votes); err != nil {
		t.Fatal(err)
	}
	return got
}

func preparationReq(round, version int) api.RoomIntentRequest {
	req := validIntentRequest()
	req.ExpectedRoundNo = round
	req.ExpectedRoomVersion = version
	return req
}

func setupPreparation(t *testing.T, builder *poolBuilderFake) (*roomFixture, *rooms.Service) {
	t.Helper()
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := db.Exec(context.Background(), "INSERT INTO room_member_round_state(room_id,user_id,round_no) VALUES($1,$2,1)", f.room, user); err != nil {
			t.Fatal(err)
		}
	}
	return f, poolService(t, db, builder)
}

func roomVersion(t *testing.T, f *roomFixture) int {
	t.Helper()
	var version int
	if err := f.db.QueryRow(context.Background(), "SELECT version FROM rooms WHERE id=$1", f.room).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func preparationPool(t *testing.T, f *roomFixture) *store.Pool {
	t.Helper()
	db, ok := f.db.(*store.Pool)
	if !ok {
		t.Fatalf("fixture DB is %T, want *store.Pool", f.db)
	}
	return db
}

func exhaustZeroRound(t *testing.T, svc *rooms.Service, f *roomFixture, round, version int, suffix string) int {
	t.Helper()
	if _, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, preparationReq(round, version), "zero-a-"+suffix); err != nil || transitioned {
		t.Fatalf("creator round=%d transitioned=%v err=%v", round, transitioned, err)
	}
	if _, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.member}, f.room, preparationReq(round, version), "zero-b-"+suffix); err != nil || !transitioned {
		t.Fatalf("member round=%d transitioned=%v err=%v", round, transitioned, err)
	}
	return roomVersion(t, f)
}

func TestPreparationRequiresBothAndKeepsPendingIntentPrivate(t *testing.T) {
	// This empty builder is intentional lifecycle isolation, not a recommendation assertion.
	builder := &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "preparation-zero", InputFingerprint: "preparation-zero"}}
	f, svc := setupPreparation(t, builder)
	version := exhaustZeroRound(t, svc, f, 1, roomVersion(t, f), "r1")

	first := preparationReq(1, version)
	first.FreeText = nullable.NewNullableWithValue("private-next-round-intent")
	if _, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, first, "prepare-a"); err != nil || transitioned {
		t.Fatalf("first preparation transitioned=%v err=%v", transitioned, err)
	}
	facts := preparationFactsFor(t, f)
	if facts.state != "exhausted" || facts.round != 1 || facts.nextReady != 1 || facts.pools != 1 || facts.votes != 0 {
		t.Fatalf("first preparation facts=%+v; want active exhausted round1, one next confirmation, one pool, zero votes", facts)
	}
	peer, err := svc.Get(context.Background(), contracts.Principal{UserID: f.member}, f.room)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(peer)
	if err != nil || strings.Contains(string(body), "private-next-round-intent") {
		t.Fatalf("peer snapshot leaked pending intent or failed marshal: body=%s err=%v", body, err)
	}

	beforeReplay := preparationFactsFor(t, f)
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, first, "prepare-a"); err != nil {
		t.Fatalf("same-key replay: %v", err)
	}
	if after := preparationFactsFor(t, f); after != beforeReplay {
		t.Fatalf("same-key replay mutated state: before=%+v after=%+v", beforeReplay, after)
	}
	mismatch := first
	mismatch.BudgetMaxMinor++
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, mismatch, "prepare-a"); !errors.Is(err, rooms.ErrIdempotencyConflict) {
		t.Fatalf("same key/different body err=%v; want idempotency conflict", err)
	}
	if after := preparationFactsFor(t, f); after != beforeReplay {
		t.Fatalf("idempotency conflict mutated state: before=%+v after=%+v", beforeReplay, after)
	}

	if _, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.member}, f.room, preparationReq(1, roomVersion(t, f)), "prepare-b"); err != nil || !transitioned {
		t.Fatalf("peer confirmation transitioned=%v err=%v", transitioned, err)
	}
	facts = preparationFactsFor(t, f)
	if facts.state != "exhausted" || facts.round != 2 || facts.pools != 2 || facts.votes != 0 || len(builder.calls()) != 2 {
		t.Fatalf("both confirmations facts=%+v builds=%d; want one build per fully-confirmed round", facts, len(builder.calls()))
	}
	// A delayed transport retry must replay the current safe snapshot, not use the
	// now-active round 2 as an implicit request to prepare round 3.
	afterAdvance := preparationFactsFor(t, f)
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, first, "prepare-a"); err != nil {
		t.Fatalf("post-activation same-key replay: %v", err)
	}
	if afterReplay := preparationFactsFor(t, f); afterReplay != afterAdvance {
		t.Fatalf("post-activation replay advanced another round: before=%+v after=%+v", afterAdvance, afterReplay)
	}
}

func TestPreparationStaleAndConcurrentDuplicateAreNoExtraApplications(t *testing.T) {
	builder := &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "preparation-zero", InputFingerprint: "preparation-zero"}}
	f, svc := setupPreparation(t, builder)
	version := exhaustZeroRound(t, svc, f, 1, roomVersion(t, f), "r1")
	old := preparationReq(1, version)
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, old, "prepare-a"); err != nil {
		t.Fatal(err)
	}
	before := preparationFactsFor(t, f)
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.member}, f.room, old, "stale-peer"); err == nil {
		t.Fatal("stale room version unexpectedly confirmed pending peer intent")
	}
	if after := preparationFactsFor(t, f); after != before {
		t.Fatalf("stale request mutated room: before=%+v after=%+v", before, after)
	}

	current := preparationReq(1, roomVersion(t, f))
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.member}, f.room, current, "prepare-b")
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("same-user same-key duplicate err=%v", err)
		}
	}
	facts := preparationFactsFor(t, f)
	if facts.round != 2 || facts.pools != 2 || facts.votes != 0 || len(builder.calls()) != 2 {
		t.Fatalf("duplicate confirmation facts=%+v builds=%d; want exactly one second-round transition", facts, len(builder.calls()))
	}
}

func TestPreparationBuilderFailureRollsBackSecondConfirmation(t *testing.T) {
	zeroBuilder := &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "preparation-zero", InputFingerprint: "preparation-zero"}}
	f, zeroService := setupPreparation(t, zeroBuilder)
	version := exhaustZeroRound(t, zeroService, f, 1, roomVersion(t, f), "r1")

	failingService := poolService(t, preparationPool(t, f), &poolBuilderFake{err: errors.New("forced next-round builder failure")})
	if _, _, err := failingService.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, preparationReq(1, version), "failure-prepare-a"); err != nil {
		t.Fatalf("first pending confirmation: %v", err)
	}
	before := preparationFactsFor(t, f)
	if before.state != "exhausted" || before.round != 1 || before.nextReady != 1 || before.pools != 1 {
		t.Fatalf("before failed build=%+v; want one valid pending confirmation", before)
	}
	if _, _, err := failingService.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.member}, f.room, preparationReq(1, roomVersion(t, f)), "failure-prepare-b"); err == nil {
		t.Fatal("second pending confirmation unexpectedly committed despite builder failure")
	}
	if after := preparationFactsFor(t, f); after != before {
		t.Fatalf("builder failure did not atomically retain pending state: before=%+v after=%+v", before, after)
	}
}

func TestPreparationConcurrentDistinctConfirmationsBuildOneNextPool(t *testing.T) {
	builder := &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "preparation-zero", InputFingerprint: "preparation-zero"}}
	f, svc := setupPreparation(t, builder)
	version := exhaustZeroRound(t, svc, f, 1, roomVersion(t, f), "r1")
	request := preparationReq(1, version)

	type result struct {
		user uuid.UUID
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, user := range []uuid.UUID{f.creator, f.member} {
		wg.Add(1)
		go func(user uuid.UUID) {
			defer wg.Done()
			<-start
			_, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: user}, f.room, request, "distinct-"+user.String())
			results <- result{user: user, err: err}
		}(user)
	}
	close(start)
	wg.Wait()
	close(results)

	var confirmed, stale uuid.UUID
	for result := range results {
		if result.err == nil {
			confirmed = result.user
			continue
		}
		stale = result.user
	}
	if confirmed == uuid.Nil || stale == uuid.Nil || confirmed == stale {
		t.Fatalf("concurrent distinct confirmations did not yield one preparation and one stale source: confirmed=%s stale=%s", confirmed, stale)
	}
	facts := preparationFactsFor(t, f)
	if facts.state != "exhausted" || facts.round != 1 || facts.nextReady != 1 || facts.pools != 1 || facts.votes != 0 {
		t.Fatalf("concurrent initial proposals facts=%+v; want one pending next-round confirmation", facts)
	}
	if _, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: stale}, f.room, preparationReq(1, roomVersion(t, f)), "distinct-fresh-"+stale.String()); err != nil || !transitioned {
		t.Fatalf("fresh confirmation of unready peer transitioned=%v err=%v", transitioned, err)
	}
	facts = preparationFactsFor(t, f)
	if facts.round != 2 || facts.pools != 2 || facts.votes != 0 || len(builder.calls()) != 2 {
		t.Fatalf("concurrent distinct completion facts=%+v builds=%d; want exactly one next pool and no votes", facts, len(builder.calls()))
	}
}

func TestPreparationZeroPoolsConsumeOnlyConfirmedRoundsUpToThree(t *testing.T) {
	builder := &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "preparation-zero", InputFingerprint: "preparation-zero"}}
	f, svc := setupPreparation(t, builder)
	version := exhaustZeroRound(t, svc, f, 1, roomVersion(t, f), "r1")
	for round := 1; round < 3; round++ {
		keySuffix := strconv.Itoa(round)
		if _, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, preparationReq(round, version), "sequence-a-"+keySuffix); err != nil || transitioned {
			t.Fatalf("round%d first preparation transitioned=%v err=%v", round, transitioned, err)
		}
		if _, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.member}, f.room, preparationReq(round, roomVersion(t, f)), "sequence-b-"+keySuffix); err != nil || !transitioned {
			t.Fatalf("round%d second preparation transitioned=%v err=%v", round, transitioned, err)
		}
		version = roomVersion(t, f)
	}
	facts := preparationFactsFor(t, f)
	if facts.state != "exhausted" || facts.round != 3 || facts.pools != 3 || facts.votes != 0 || len(builder.calls()) != 3 {
		t.Fatalf("three zero-candidate rounds facts=%+v builds=%d; want exhausted round3 with three pools and zero votes", facts, len(builder.calls()))
	}
	beforeTerminalRetry := preparationFactsFor(t, f)
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, preparationReq(3, version), "round-three-retry"); err == nil {
		t.Fatal("round-three preparation unexpectedly opened a fourth round")
	}
	if afterTerminalRetry := preparationFactsFor(t, f); afterTerminalRetry != beforeTerminalRetry {
		t.Fatalf("terminal retry mutated exhausted round3: before=%+v after=%+v", beforeTerminalRetry, afterTerminalRetry)
	}
}

func TestPreparationHTTPRequiresIdempotencyKeyAndSourceFields(t *testing.T) {
	builder := &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "preparation-zero", InputFingerprint: "preparation-zero"}}
	f, svc := setupPreparation(t, builder)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: f.creator})))
		})
	})
	svc.RegisterRoutes(router)
	valid, err := json.Marshal(preparationReq(1, roomVersion(t, f)))
	if err != nil {
		t.Fatal(err)
	}
	missingSource := map[string]json.RawMessage{}
	if err := json.Unmarshal(valid, &missingSource); err != nil {
		t.Fatal(err)
	}
	delete(missingSource, "expected_room_version")
	missingSourceBody, err := json.Marshal(missingSource)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		body string
		key  string
	}{
		"missing idempotency key":       {body: string(valid)},
		"short idempotency key":         {body: string(valid), key: "short"},
		"missing expected room version": {body: string(missingSourceBody), key: "valid-key-123"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/api/v1/rooms/"+f.room.String()+"/intent/me", strings.NewReader(tc.body))
			if tc.key != "" {
				request.Header.Set("Idempotency-Key", tc.key)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"code":"VALIDATION_FAILED"`) {
				t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}
