package integration

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

type poolBuilderFake struct {
	mu            sync.Mutex
	result        contracts.BuildResult
	err           error
	inputs        []contracts.BuildInput
	started       chan struct{}
	continueBuild chan struct{}
}

// poolBuilderPoolProbe models a production builder that performs a catalog
// read through its database pool. With MaxConns=1 this catches holding the
// room transaction open while calling the builder: the probe must complete
// instead of waiting for a second connection.
type poolBuilderPoolProbe struct {
	db    *store.Pool
	event uuid.UUID
}

func (b poolBuilderPoolProbe) Build(ctx context.Context, input contracts.BuildInput) (contracts.BuildResult, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	var one int
	dbtx := store.DBTXFromContext(probeCtx)
	if dbtx == nil {
		dbtx = b.db
	}
	if err := dbtx.QueryRow(probeCtx, "SELECT 1").Scan(&one); err != nil {
		return contracts.BuildResult{}, err
	}
	return contracts.BuildResult{
		Candidates:       []contracts.Candidate{{EventID: b.event}},
		RankerVersion:    "pool-probe",
		InputFingerprint: "pool-probe",
	}, nil
}

func (f *poolBuilderFake) Build(ctx context.Context, input contracts.BuildInput) (contracts.BuildResult, error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, cloneBuildInput(input))
	started := f.started
	continueBuild := f.continueBuild
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if continueBuild != nil {
		select {
		case <-continueBuild:
		case <-ctx.Done():
			return contracts.BuildResult{}, ctx.Err()
		}
	}
	return f.result, f.err
}

func (f *poolBuilderFake) calls() []contracts.BuildInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]contracts.BuildInput, len(f.inputs))
	copy(result, f.inputs)
	return result
}

func cloneBuildInput(in contracts.BuildInput) contracts.BuildInput {
	in.PreviousEventIDs = append([]uuid.UUID(nil), in.PreviousEventIDs...)
	clone := func(v []string) []string { return append([]string(nil), v...) }
	for _, inPtr := range []*contracts.ParticipantIntent{&in.FirstIntent, &in.SecondIntent} {
		inPtr.Dates = clone(inPtr.Dates)
		inPtr.DayTypes = clone(inPtr.DayTypes)
		inPtr.TimeSlots = clone(inPtr.TimeSlots)
		inPtr.CategorySlugs = clone(inPtr.CategorySlugs)
		inPtr.ExclusionSlugs = clone(inPtr.ExclusionSlugs)
		if inPtr.RadiusM != nil {
			v := *inPtr.RadiusM
			inPtr.RadiusM = &v
		}
		if inPtr.Location != nil {
			v := *inPtr.Location
			inPtr.Location = &v
		}
		if inPtr.FreeText != nil {
			v := *inPtr.FreeText
			inPtr.FreeText = &v
		}
	}
	return in
}

func poolService(t *testing.T, db *store.Pool, builder contracts.PoolBuilder) *rooms.Service {
	t.Helper()
	invites, err := rooms.NewInviteCodec([]byte("0123456789abcdef0123456789abcdef"), 1, "https://app.test/invite/{token}", "https://max.test/bot?startapp={token}")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := rooms.NewCreateService(db, behavior.Recorder{}, invites, builder)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func submitBothIntents(t *testing.T, svc *rooms.Service, f *roomFixture) (bool, error) {
	t.Helper()
	_, transitioned, err := submitBothIntentsSnapshots(t, svc, f)
	return transitioned, err
}

func submitBothIntentsSnapshots(t *testing.T, svc *rooms.Service, f *roomFixture) (api.RoomSnapshot, bool, error) {
	t.Helper()
	_, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, validIntentRequest())
	if err != nil {
		return api.RoomSnapshot{}, transitioned, err
	}
	snapshot, transitioned, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.member}, f.room, validIntentRequest())
	return snapshot, transitioned, err
}

func assertExhaustionReasons(t *testing.T, snapshot api.RoomSnapshot, wantCodes []string) {
	t.Helper()
	pool, err := snapshot.Pool.Get()
	if err != nil {
		t.Fatalf("pool missing: %v", err)
	}
	if pool.ExhaustionReasons == nil {
		t.Fatalf("exhaustion reasons missing in snapshot: %+v", pool)
	}
	if len(*pool.ExhaustionReasons) != len(wantCodes) {
		t.Fatalf("exhaustion reasons=%+v, want codes=%v", *pool.ExhaustionReasons, wantCodes)
	}
	for i, want := range wantCodes {
		if string((*pool.ExhaustionReasons)[i].Code) != want || (*pool.ExhaustionReasons)[i].Text == "" {
			t.Fatalf("exhaustion reason[%d]=%+v, want code=%q and text", i, (*pool.ExhaustionReasons)[i], want)
		}
	}
}

func TestB6PoolBuilderScenariosAndPersistence(t *testing.T) {
	cases := []struct {
		name      string
		count     int
		exhausted bool
	}{
		{name: "normal", count: 3},
		{name: "small", count: 2},
		{name: "zero", exhausted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			f := newRoomFixture(t, db)
			f.addTwoMembers(t)
			for _, user := range []uuid.UUID{f.creator, f.member} {
				if _, err := db.Exec(context.Background(), "INSERT INTO room_member_round_state(room_id,user_id,round_no) VALUES($1,$2,1)", f.room, user); err != nil {
					t.Fatal(err)
				}
			}
			ids := make([]uuid.UUID, tc.count)
			for i := range ids {
				ids[i] = f.newEvent(t)
			}
			builder := &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "fake-v1", InputFingerprint: "fp-" + tc.name}}
			if tc.exhausted {
				builder.result.Diagnostics.Reasons = []contracts.ExhaustionReason{{Code: "catalog_shortage", Text: "Недостаточно подходящих событий в каталоге."}}
			}
			for _, id := range ids {
				builder.result.Candidates = append(builder.result.Candidates, contracts.Candidate{EventID: id, Score: contracts.ScoreSnapshot{GroupScore: .75, ParticipantScoreMin: .2, ParticipantScoreMean: .3}, Explanation: []contracts.Explanation{{Code: "popular", Text: "safe"}}, FeatureSnapshot: contracts.FeatureSnapshot{"x": 1}})
			}
			svc := poolService(t, db, builder)
			immediate, transitioned, err := submitBothIntentsSnapshots(t, svc, f)
			if err != nil || !transitioned {
				t.Fatalf("submit intents transitioned=%v err=%v", transitioned, err)
			}
			var state string
			var active, count int
			var small bool
			if err := db.QueryRow(context.Background(), "SELECT state,active_pool_version FROM rooms WHERE id=$1", f.room).Scan(&state, &active); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(context.Background(), "SELECT candidate_count,is_small FROM room_pools WHERE room_id=$1", f.room).Scan(&count, &small); err != nil {
				t.Fatal(err)
			}
			wantState := "voting"
			if tc.exhausted {
				wantState = "exhausted"
			}
			if state != wantState || active != 1 || count != tc.count || small != (tc.count > 0 && tc.count < 3) {
				t.Fatalf("room/pool state=%s/%d count=%d small=%v", state, active, count, small)
			}
			var positions []int
			rows, err := db.Query(context.Background(), "SELECT position FROM room_pool_events WHERE pool_id=(SELECT id FROM room_pools WHERE room_id=$1) ORDER BY position", f.room)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var p int
				if err := rows.Scan(&p); err != nil {
					t.Fatal(err)
				}
				positions = append(positions, p)
			}
			for i, p := range positions {
				if p != i {
					t.Fatalf("positions=%v", positions)
				}
			}
			if tc.count > 0 {
				var score float32
				var explanation, features []byte
				if err := db.QueryRow(context.Background(), "SELECT group_score,explanation,feature_snapshot FROM room_pool_events WHERE pool_id=(SELECT id FROM room_pools WHERE room_id=$1) AND position=0", f.room).Scan(&score, &explanation, &features); err != nil {
					t.Fatal(err)
				}
				if score == 0 || string(explanation) != `[{"code":"popular","text":"safe"}]` || string(features) != `{"x":1}` {
					t.Fatalf("persisted event snapshot score=%v explanation=%s features=%s", score, explanation, features)
				}
			}
			if tc.exhausted && len(positions) != 0 {
				t.Fatalf("zero-candidate pool persisted events=%v", positions)
			}
			if tc.exhausted {
				assertExhaustionReasons(t, immediate, []string{"catalog_shortage"})
				reconnected, err := svc.Get(context.Background(), contracts.Principal{UserID: f.creator}, f.room)
				if err != nil {
					t.Fatal(err)
				}
				assertExhaustionReasons(t, reconnected, []string{"catalog_shortage"})
			}
		})
	}
}

func TestB6BuildInputPrivacyVersionRoundCityAndPreviousIDs(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := db.Exec(context.Background(), "INSERT INTO room_member_round_state(room_id,user_id,round_no) VALUES($1,$2,1)", f.room, user); err != nil {
			t.Fatal(err)
		}
	}
	old := f.newEvent(t)
	if _, err := db.Exec(context.Background(), "INSERT INTO room_pools(id,room_id,version,round_no,ranker_version,input_fingerprint,state,candidate_count) VALUES($1,$2,1,1,'old','old-fp','ready',1)", uuid.New(), f.room); err != nil {
		t.Fatal(err)
	}
	oldPoolID := uuid.New()
	if _, err := db.Exec(context.Background(), "UPDATE room_pools SET id=$1 WHERE room_id=$2", oldPoolID, f.room); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), "INSERT INTO room_pool_events(pool_id,event_id,position,group_score,participant_score_min,participant_score_mean,explanation,feature_snapshot) VALUES($1,$2,0,0,0,0,'{}','{}')", oldPoolID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), "UPDATE rooms SET active_pool_version=0 WHERE id=$1", f.room); err != nil {
		t.Fatal(err)
	}
	builder := &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "fake", InputFingerprint: "new-fp", Candidates: []contracts.Candidate{{EventID: f.newEvent(t)}}}}
	svc := poolService(t, db, builder)
	if _, err := submitBothIntents(t, svc, f); err != nil {
		t.Fatal(err)
	}
	inputs := builder.calls()
	if len(inputs) != 1 {
		t.Fatalf("Build calls=%d", len(inputs))
	}
	in := inputs[0]
	if in.RoomID != f.room || in.CityID != f.city || in.RoundNo != 1 || in.PoolVersion != 1 || !reflect.DeepEqual(in.PreviousEventIDs, []uuid.UUID{old}) {
		t.Fatalf("BuildInput metadata=%+v", in)
	}
	intentsByUser := map[uuid.UUID]contracts.ParticipantIntent{
		in.FirstIntent.UserID:  in.FirstIntent,
		in.SecondIntent.UserID: in.SecondIntent,
	}
	if len(intentsByUser) != 2 || intentsByUser[f.creator].Version != 1 || intentsByUser[f.member].Version != 1 {
		t.Fatalf("BuildInput users/versions=%+v", in)
	}
	for user, intent := range intentsByUser {
		if intent.FreeText == nil || *intent.FreeText != "private preference" {
			t.Fatalf("private intent for %s missing: %+v", user, intent)
		}
	}
}

func TestB6BuilderErrorRollsBackPoolAndSecondIntent(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := db.Exec(context.Background(), "INSERT INTO room_member_round_state(room_id,user_id,round_no) VALUES($1,$2,1)", f.room, user); err != nil {
			t.Fatal(err)
		}
	}
	builder := &poolBuilderFake{err: errors.New("builder failed")}
	svc := poolService(t, db, builder)
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, validIntentRequest()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.member}, f.room, validIntentRequest()); err == nil {
		t.Fatal("builder error was swallowed")
	}
	var state string
	var intents, pools int
	if err := db.QueryRow(context.Background(), "SELECT state FROM rooms WHERE id=$1", f.room).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM room_intents WHERE room_id=$1", f.room).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM room_pools WHERE room_id=$1", f.room).Scan(&pools); err != nil {
		t.Fatal(err)
	}
	if state != "collecting_intents" || intents != 1 || pools != 0 {
		t.Fatalf("rollback state=%s intents=%d pools=%d", state, intents, pools)
	}
}

func TestB6MalformedOrUnknownDiagnosticsAreSafeOnReconnect(t *testing.T) {
	for _, tc := range []struct {
		name        string
		diagnostics string
	}{
		{name: "malformed", diagnostics: `{"reasons":`},
		{name: "unknown code", diagnostics: `{"reasons":[{"code":"private_intent","text":"must not leak"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			f := newRoomFixture(t, db)
			f.addTwoMembers(t)
			poolID := uuid.New()
			if _, err := db.Exec(context.Background(), `
				INSERT INTO room_pools(id,room_id,version,round_no,ranker_version,input_fingerprint,state,candidate_count,diagnostics)
				VALUES($1,$2,1,1,'test','diagnostics-test','exhausted',0,$3)`, poolID, f.room, []byte(tc.diagnostics)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(context.Background(), "UPDATE rooms SET state='exhausted',active_pool_version=1 WHERE id=$1", f.room); err != nil {
				t.Fatal(err)
			}
			svc := poolService(t, db, &poolBuilderFake{})
			snapshot, err := svc.Get(context.Background(), contracts.Principal{UserID: f.creator}, f.room)
			if err != nil {
				t.Fatalf("Get with %s diagnostics: %v", tc.name, err)
			}
			pool, err := snapshot.Pool.Get()
			if err != nil {
				t.Fatalf("pool missing: %v", err)
			}
			if pool.ExhaustionReasons != nil && len(*pool.ExhaustionReasons) != 0 {
				t.Fatalf("unsafe diagnostics exposed for %s: %+v", tc.name, *pool.ExhaustionReasons)
			}
		})
	}
}

func openSingleConnTestDB(t *testing.T) *store.Pool {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open single-connection test database: %v", err)
	}
	db := &store.Pool{Pool: pool}
	if err := db.Ping(context.Background()); err != nil {
		db.Close()
		t.Fatalf("ping single-connection test database: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestB6ProductionLikeBuilderDoesNotNeedNestedPoolCheckout(t *testing.T) {
	db := openSingleConnTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := db.Exec(context.Background(), "INSERT INTO room_member_round_state(room_id,user_id,round_no) VALUES($1,$2,1)", f.room, user); err != nil {
			t.Fatal(err)
		}
	}
	event := f.newEvent(t)
	svc := poolService(t, db, poolBuilderPoolProbe{db: db, event: event})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: f.creator}, f.room, validIntentRequest()); err != nil {
		t.Fatal(err)
	}
	if _, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: f.member}, f.room, validIntentRequest()); err != nil || !transitioned {
		t.Fatalf("second intent transitioned=%v err=%v", transitioned, err)
	}
}

func TestB6BuilderDeterminismAndNoDuplicateConcurrentBuild(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := db.Exec(context.Background(), "INSERT INTO room_member_round_state(room_id,user_id,round_no) VALUES($1,$2,1)", f.room, user); err != nil {
			t.Fatal(err)
		}
	}
	event := f.newEvent(t)
	builder := &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "fake", InputFingerprint: "stable", Candidates: []contracts.Candidate{{EventID: event}}}}
	svc := poolService(t, db, builder)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, user := range []uuid.UUID{f.creator, f.member} {
		wg.Add(1)
		go func(user uuid.UUID) {
			defer wg.Done()
			<-start
			_, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: user}, f.room, validIntentRequest())
			errs <- err
		}(user)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := len(builder.calls()); got != 1 {
		t.Fatalf("concurrent Build calls=%d, want 1", got)
	}
	var pools int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM room_pools WHERE room_id=$1", f.room).Scan(&pools); err != nil || pools != 1 {
		t.Fatalf("pool rows=%d err=%v", pools, err)
	}
}
