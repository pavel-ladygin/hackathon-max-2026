package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/analytics"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

type recommendationKPIExport struct {
	Version         string            `json:"metrics_version"`
	Snapshot        time.Time         `json:"snapshot_at"`
	Rooms           map[string]int    `json:"room_counts"`
	Rounds          map[string]int    `json:"round_counts"`
	RateDefinitions map[string]string `json:"rate_definitions"`
	Rates           map[string]struct {
		N    int      `json:"numerator"`
		D    int      `json:"denominator"`
		Rate *float64 `json:"rate"`
	} `json:"rates"`
}

func openRecommendationAnalyticsDB(t *testing.T) *store.Pool {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url != "" {
		cfg, err := pgx.ParseConfig(url)
		if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
			t.Fatal("analytics fixtures require a dedicated database ending in _test")
		}
	}
	return openTestDB(t)
}

func TestRecommendationAnalyticsRoomRoundAttributionAndExport(t *testing.T) {
	db := openRecommendationAnalyticsDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	f.db = tx
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var snapshot time.Time
	if err := tx.QueryRow(ctx, "SELECT now()").Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	created, joined := snapshot.Add(-5*time.Hour), snapshot.Add(-5*time.Hour+2*time.Minute)
	generated, matched := snapshot.Add(-4*time.Hour), snapshot.Add(-3*time.Hour)
	type fixtureRoom struct {
		id, creator, member uuid.UUID
		pools               []uuid.UUID
		event               uuid.UUID
	}
	rooms := make([]fixtureRoom, 8)
	for i := range rooms {
		creator, member, room := f.creator, f.member, f.room
		if i != 0 {
			creator, member = f.newUser(t), f.newUser(t)
			room = f.insertRoom(t, creator)
		}
		rooms[i] = fixtureRoom{id: room, creator: creator, member: member}
		exec("UPDATE rooms SET created_at=$2,expires_at=$3 WHERE id=$1", room, created, snapshot.Add(time.Hour))
		exec("INSERT INTO room_members(room_id,user_id,role,joined_at) VALUES($1,$2,'creator',$3)", room, creator, created)
		if i != 5 && i != 6 {
			exec("INSERT INTO room_members(room_id,user_id,role,joined_at) VALUES($1,$2,'participant',$3)", room, member, joined)
		}
	}
	pool := func(index, round, version, count int, exhausted bool) uuid.UUID {
		t.Helper()
		r := &rooms[index]
		id := uuid.New()
		state := "ready"
		if exhausted {
			state = "exhausted"
		}
		exec(`INSERT INTO room_pools(id,room_id,version,round_no,ranker_version,input_fingerprint,state,candidate_count,created_at)
		 VALUES($1,$2,$3,$4,'synthetic-analytics-test',$5,$6,$7,$8)`, id, r.id, version, round, uuid.NewString(), state, count, generated.Add(time.Duration(version)*time.Minute))
		if count > 0 {
			r.event = f.newEvent(t)
			exec(`INSERT INTO room_pool_events(pool_id,event_id,position,group_score,participant_score_min,participant_score_mean,explanation,feature_snapshot)
			 VALUES($1,$2,0,1,1,1,'[]','{}')`, id, r.event)
		}
		r.pools = append(r.pools, id)
		exec("UPDATE rooms SET round_no=$2,active_pool_version=$3,state=$4 WHERE id=$1", r.id, round, version, map[bool]string{true: "exhausted", false: "voting"}[exhausted])
		return id
	}
	// Eight rooms: zero->round2 match; round3 recovery; restartable exhausted;
	// one-sided waiting; third-round terminal; closed; creator-only; first match.
	pool(0, 1, 1, 0, true)
	pool(0, 2, 2, 1, false)
	pool(1, 1, 1, 1, true)
	pool(1, 2, 2, 1, true)
	pool(1, 3, 3, 1, false)
	pool(2, 1, 1, 1, true)
	pool(3, 1, 1, 1, false)
	pool(3, 1, 2, 1, false) // Same round, two pool versions: count once.
	pool(4, 1, 1, 0, true)
	pool(4, 2, 2, 0, true)
	pool(4, 3, 3, 1, true)
	exec("UPDATE rooms SET state='closed',closed_by=$2,closed_at=$3 WHERE id=$1", rooms[5].id, rooms[5].creator, matched)
	exec("UPDATE rooms SET expires_at=$2 WHERE id=$1", rooms[6].id, snapshot.Add(-time.Hour))
	pool(7, 1, 1, 1, false)
	for _, i := range []int{0, 1, 2, 3, 4, 7} {
		r := rooms[i]
		voters := []uuid.UUID{r.creator, r.member}
		if i == 3 {
			voters = voters[:1]
		}
		for _, user := range voters {
			vote := "dislike"
			if i == 0 || i == 1 || i == 7 {
				vote = "like"
			}
			exec(`INSERT INTO room_votes(pool_id,room_id,event_id,user_id,vote,created_at)
			 VALUES($1,$2,$3,$4,$5,$6)`, r.pools[len(r.pools)-1], r.id, r.event, user, vote, generated.Add(time.Hour/2))
		}
	}
	for _, i := range []int{0, 1, 7} {
		r := rooms[i]
		exec("UPDATE rooms SET state='matched',matched_event_id=$2 WHERE id=$1", r.id, r.event)
		exec("INSERT INTO room_matches(id,room_id,pool_id,event_id,matched_at) VALUES($1,$2,$3,$4,$5)", uuid.New(), r.id, r.pools[len(r.pools)-1], r.event, matched)
	}
	// Historical participants become inactive after terminal outcomes; their
	// confirmed historical roles still count. Votes alone never define intent activation.
	exec("UPDATE room_members SET is_active=false WHERE room_id=ANY($1::uuid[])", []uuid.UUID{rooms[0].id, rooms[1].id, rooms[4].id, rooms[5].id, rooms[7].id})
	// A room outside the room-created cohort still contributes a generated round.
	oldCreator := f.newUser(t)
	oldRoom := f.insertRoom(t, oldCreator)
	exec("UPDATE rooms SET created_at=$2 WHERE id=$1", oldRoom, snapshot.AddDate(0, 0, -40))
	exec(`INSERT INTO room_pools(id,room_id,version,round_no,ranker_version,input_fingerprint,state,candidate_count,created_at)
	 VALUES($1,$2,1,1,'synthetic-analytics-test',$3,'ready',1,$4)`, uuid.New(), oldRoom, uuid.NewString(), generated)
	// A ranking draft and a future-generated pool are not generated rounds now.
	for _, state := range []string{"ranking", "ready"} {
		when, version := generated, 1
		if state == "ready" {
			when, version = snapshot.Add(time.Hour), 2
		}
		exec(`INSERT INTO room_pools(id,room_id,version,round_no,ranker_version,input_fingerprint,state,candidate_count,created_at)
		 VALUES($1,$2,$3,1,'synthetic-analytics-test',$4,$5,1,$6)`, uuid.New(), rooms[6].id, version, uuid.NewString(), state, when)
	}
	behavior := func(room, user uuid.UUID, origin, typ string, event *uuid.UUID, occurred, received time.Time, props string) {
		t.Helper()
		exec(`INSERT INTO behavior_events(id,user_id,origin,type,room_id,event_id,client_event_id,occurred_at,received_at,properties)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb)`, uuid.New(), user, origin, typ, room, event, uuid.NewString(), occurred, received, props)
	}
	r := rooms[0]
	previewAt := created.Add(time.Minute)
	inviteHash := uuid.New()
	exec(`INSERT INTO room_invites(id,room_id,token_hash,token_ciphertext,encryption_key_version,created_by,created_at,expires_at,consumed_by,consumed_at)
	 VALUES($1,$2,$3,'synthetic',1,$4,$5,$6,$7,$8)`, uuid.New(), r.id, inviteHash[:], r.creator, created, matched, r.member, joined)
	for range 2 {
		behavior(r.id, r.creator, "client", "invite_shared", nil, created.Add(30*time.Second), created.Add(30*time.Second), `{}`)
		behavior(r.id, r.member, "client", "invite_link_opened", nil, previewAt, previewAt, `{}`)
		behavior(r.id, r.member, "client", "match_opened", &r.event, matched.Add(time.Minute), matched.Add(time.Minute), `{}`)
		behavior(r.id, r.member, "server", "ticket_click", &r.event, matched.Add(time.Minute), matched.Add(time.Minute), `{}`)
	}
	// Creator view, unverified recipient claim, share by a noncreator,
	// nonmember click, wrong event, pre-match click and backdated receipt cannot
	// manufacture a new converting room. Spoof a different matched room too.
	bad := rooms[1]
	badInviteHash := uuid.New()
	exec(`INSERT INTO room_invites(id,room_id,token_hash,token_ciphertext,encryption_key_version,created_by,created_at,expires_at,consumed_by,consumed_at)
	 VALUES($1,$2,$3,'synthetic',1,$4,$5,$6,$7,$8)`, uuid.New(), bad.id, badInviteHash[:], bad.creator, created, matched, bad.member, joined)
	behavior(rooms[6].id, rooms[6].creator, "client", "invite_opened", nil, previewAt, previewAt, `{}`)
	behavior(rooms[6].id, f.third, "client", "invite_link_opened", nil, previewAt, previewAt, `{"role":"recipient","context":"pre_join"}`)
	behavior(rooms[6].id, f.third, "client", "invite_shared", nil, previewAt, previewAt, `{}`)
	behavior(bad.id, f.third, "client", "match_opened", &bad.event, matched.Add(time.Minute), matched.Add(time.Minute), `{}`)
	behavior(bad.id, bad.member, "client", "match_opened", &r.event, matched.Add(time.Minute), matched.Add(time.Minute), `{}`)
	behavior(bad.id, bad.member, "client", "match_opened", &bad.event, matched.Add(-time.Minute), matched.Add(-time.Minute), `{}`)
	behavior(bad.id, bad.member, "client", "match_opened", &bad.event, matched.Add(time.Minute), matched.Add(-time.Minute), `{}`)
	behavior(r.id, r.member, "client", "invite_opened", nil, joined.Add(time.Minute), joined.Add(time.Minute), `{}`)
	// A forged pre-join client timestamp delivered after joining is not proof.
	behavior(bad.id, bad.member, "client", "invite_link_opened", nil, previewAt, matched, `{}`)
	behavior(bad.id, bad.member, "server", "ticket_click", &r.event, matched.Add(time.Minute), matched.Add(time.Minute), `{}`)
	behavior(bad.id, f.third, "server", "ticket_click", &bad.event, matched.Add(time.Minute), matched.Add(time.Minute), `{}`)
	behavior(bad.id, bad.member, "server", "ticket_click", &bad.event, snapshot.Add(time.Hour), snapshot.Add(time.Hour), `{}`)

	response := httptest.NewRecorder()
	analytics.NewHandler(tx).GetRecommendationAnalytics(response, httptest.NewRequest("GET", "/api/v1/internal/analytics/recommendations?days=7", nil))
	if response.Code != 200 {
		t.Fatalf("typed KPI export failed: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.Bytes()
	var export recommendationKPIExport
	if err := json.Unmarshal(body, &export); err != nil {
		t.Fatal(err)
	}
	if export.Version != "recommendation-analytics-v2" || !export.Snapshot.Equal(snapshot) {
		t.Fatal("missing stable observation metadata")
	}
	for name, want := range map[string]int{
		"room_created": 8, "second_user_joined": 6, "both_intents_ready": 6, "both_distinct_users_voted": 5, "room_matched": 3,
		"completed_no_match": 1, "incomplete": 3, "expired_incomplete": 1, "closed": 1, "first_round_match": 1,
		"recovered_round_2": 1, "recovered_round_3": 1, "invite_share_attempt_rooms": 1, "recipient_preview_rooms": 1,
		"match_event_open_click_rooms": 1, "match_event_open_clicks": 2, "ticket_click_rooms": 1, "ticket_clicks": 2, "rooms_with_test_events": 6,
	} {
		if export.Rooms[name] != want {
			t.Errorf("%s=%d, hand-counted=%d", name, export.Rooms[name], want)
		}
	}
	for name, want := range map[string]int{"generated_rounds": 12, "zero_pool_rounds": 3, "nonempty_rounds_generated": 9, "nonempty_rounds_exhausted": 4} {
		if export.Rounds[name] != want {
			t.Errorf("%s=%d, hand-counted=%d", name, export.Rounds[name], want)
		}
	}
	for name, want := range map[string][2]int{
		"room_join": {6, 8}, "room_activation": {6, 8}, "both_users_voted": {5, 8}, "created_to_match": {3, 8}, "activated_to_match": {3, 6},
		"completed_no_match": {1, 4}, "zero_pool_rounds": {3, 12}, "nonempty_rounds_exhausted": {4, 9},
		"recipient_preview_after_share": {1, 1}, "match_event_open_click": {1, 3}, "ticket_click": {1, 3},
	} {
		got := export.Rates[name]
		if export.RateDefinitions[name] == "" {
			t.Errorf("missing semantic N/D definition for %s", name)
		}
		if got.N != want[0] || got.D != want[1] || got.Rate == nil || math.Abs(*got.Rate-float64(want[0])/float64(want[1])) > 1e-9 {
			t.Errorf("%s=%+v, hand-counted=%d/%d", name, got, want[0], want[1])
		}
	}
	// Legacy views retain their previous attribution: this distinction is
	// deliberate, rather than silently rewriting the micro-pilot baseline.
	var legacyOpened, newOpened bool
	if err := tx.QueryRow(ctx, `SELECT l.invite_opened,n.recipient_previewed FROM analytics_room_metrics l
	 JOIN analytics_recommendation_rooms n USING(room_id) WHERE room_id=$1`, rooms[6].id).Scan(&legacyOpened, &newOpened); err != nil {
		t.Fatal(err)
	}
	if !legacyOpened || newOpened {
		t.Fatal("legacy preservation or creator-preview exclusion failed")
	}
	// The private export route returns aggregates plus definitions and supports
	// downloading, without exposing any fixture room/user/event IDs.
	h := analytics.NewHandler(tx)
	rec := httptest.NewRecorder()
	h.GetRecommendationAnalytics(rec, httptest.NewRequest("GET", "/api/v1/internal/analytics/recommendations?days=7&download=1", nil))
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "recommendation-kpis-v2.json") {
		t.Fatalf("export status=%d: %s", rec.Code, rec.Body.String())
	}
	for _, private := range []string{r.id.String(), r.creator.String(), r.member.String(), r.event.String(), "synthetic-analytics-test"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("aggregate export leaked an identifier")
		}
	}
	for _, required := range []string{"definitions", "rate_definitions", "release_revision", "legacy_baseline", "9/19", "5/9", "5/19", "5/5", "2/5", "unverified_claims"} {
		if !strings.Contains(rec.Body.String(), required) {
			t.Errorf("missing export context %s", required)
		}
	}
	if strings.Contains(rec.Body.String(), "integration_requests") {
		t.Fatal("integration notes remain in the KPI response")
	}
	// Move only this synthetic fixture outside every supported calendar window
	// and exercise the actual HTTP/Go/SQL path for a truly empty cohort.
	exec("UPDATE rooms SET created_at=$2 WHERE city_id=$1", f.city, snapshot.AddDate(0, 0, -100))
	exec("UPDATE room_pools SET created_at=$2 WHERE room_id IN (SELECT id FROM rooms WHERE city_id=$1)", f.city, snapshot.AddDate(0, 0, -100))
	emptyResponse := httptest.NewRecorder()
	analytics.NewHandler(tx).GetRecommendationAnalytics(emptyResponse, httptest.NewRequest("GET", "/api/v1/internal/analytics/recommendations?days=7", nil))
	if emptyResponse.Code != 200 {
		t.Fatalf("empty cohort failed: %s", emptyResponse.Body.String())
	}
	var emptyExport recommendationKPIExport
	if err := json.Unmarshal(emptyResponse.Body.Bytes(), &emptyExport); err != nil {
		t.Fatal(err)
	}
	for name, ratio := range emptyExport.Rates {
		if ratio.N != 0 || ratio.D != 0 || ratio.Rate != nil {
			t.Errorf("empty cohort manufactured %s: %+v", name, ratio)
		}
	}
}

func TestRecommendationAnalyticsIntentReadinessAndZeroPoolNeedNoVotes(t *testing.T) {
	db := openRecommendationAnalyticsDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	joined, ready := now.Add(-time.Hour), now.Add(-30*time.Minute)
	exec("UPDATE rooms SET created_at=$2 WHERE id=$1", f.room, joined)
	exec("INSERT INTO room_members(room_id,user_id,role,joined_at) VALUES($1,$2,'creator',$4),($1,$3,'participant',$4)", f.room, f.creator, f.member, joined)
	exec("INSERT INTO room_member_round_state(room_id,user_id,round_no,ready,ready_at) VALUES($1,$2,1,true,$3)", f.room, f.creator, ready)
	assertFacts := func(wantReady bool, wantOutcome string) {
		t.Helper()
		var ready, voted bool
		var outcome string
		if err := tx.QueryRow(ctx, `SELECT both_intents_ready_at IS NOT NULL,both_distinct_users_voted_at IS NOT NULL,outcome
		 FROM analytics_recommendation_rooms WHERE room_id=$1`, f.room).Scan(&ready, &voted, &outcome); err != nil {
			t.Fatal(err)
		}
		if ready != wantReady || voted || outcome != wantOutcome {
			t.Fatalf("ready=%t voted=%t outcome=%s", ready, voted, outcome)
		}
	}
	assertFacts(false, "incomplete")
	exec("INSERT INTO room_member_round_state(room_id,user_id,round_no,ready,ready_at) VALUES($1,$2,1,true,$3)", f.room, f.member, ready)
	assertFacts(true, "incomplete")
	for round := 1; round <= 3; round++ {
		exec(`INSERT INTO room_pools(id,room_id,version,round_no,ranker_version,input_fingerprint,state,candidate_count,created_at)
		 VALUES($1,$2,$3::integer,$3::smallint,'synthetic-zero',$4,'exhausted',0,$5)`, uuid.New(), f.room, round, uuid.NewString(), ready)
		exec("UPDATE rooms SET state='exhausted',round_no=$2::smallint,active_pool_version=$2::integer WHERE id=$1", f.room, round)
		outcome := "incomplete"
		if round == 3 {
			outcome = "completed_no_match"
		}
		assertFacts(true, outcome)
	}
}

func TestRecommendationAnalyticsMigrationIsAdditiveAndReversible(t *testing.T) {
	db := openRecommendationAnalyticsDB(t)
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var before, after string
	if err := tx.QueryRow(ctx, "SELECT pg_get_viewdef('analytics_room_funnel_metrics'::regclass)").Scan(&before); err != nil {
		t.Fatal(err)
	}
	data, err := migrations.Files.ReadFile("000032_recommendation_analytics.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing migration Down section")
	}
	if _, err := tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	var oldExists, newAbsent bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('analytics_room_funnel_metrics') IS NOT NULL,to_regclass('analytics_recommendation_rooms') IS NULL").Scan(&oldExists, &newAbsent); err != nil {
		t.Fatal(err)
	}
	if !oldExists || !newAbsent {
		t.Fatal("Down affected legacy analytics or retained v2 views")
	}
	if _, err := tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "SELECT pg_get_viewdef('analytics_room_funnel_metrics'::regclass)").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("migration rewrote a legacy view")
	}
}

func TestRecommendationAnalyticsUTCCreationWindowBoundaries(t *testing.T) {
	for _, days := range []int{7, 30, 90} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			db := openRecommendationAnalyticsDB(t)
			f := newRoomFixture(t, db)
			ctx := context.Background()
			tx, err := db.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			f.db = tx
			var now time.Time
			if err := tx.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
				t.Fatal(err)
			}
			utc := now.UTC()
			midnight := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
			start, end := midnight.AddDate(0, 0, -days+1), midnight.AddDate(0, 0, 1)
			for i, created := range []time.Time{start, start.Add(-time.Microsecond), end, now.Add(time.Hour)} {
				room := f.room
				if i > 0 {
					room = f.insertRoom(t, f.newUser(t))
				}
				if _, err := tx.Exec(ctx, "UPDATE rooms SET created_at=$2 WHERE id=$1", room, created); err != nil {
					t.Fatal(err)
				}
			}
			response := httptest.NewRecorder()
			analytics.NewHandler(tx).GetRecommendationAnalytics(response, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/internal/analytics/recommendations?days=%d", days), nil))
			if response.Code != 200 {
				t.Fatalf("export failed: %s", response.Body.String())
			}
			var result struct {
				PeriodStart time.Time      `json:"period_start"`
				PeriodEnd   time.Time      `json:"period_end"`
				Snapshot    time.Time      `json:"snapshot_at"`
				Rooms       map[string]int `json:"room_counts"`
				Timezone    string         `json:"timezone"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !result.PeriodStart.Equal(start) || !result.PeriodEnd.Equal(end) || !result.Snapshot.Equal(now) || result.Timezone != "UTC" {
				t.Fatal("report window does not match the requested UTC calendar period")
			}
			if result.Rooms["room_created"] != 1 || result.Rooms["incomplete"] != 1 {
				t.Fatalf("creation cohort includes a boundary/future room: %+v", result.Rooms)
			}
		})
	}
}
