package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeDB struct {
	calls   int
	days    any
	payload []byte
	err     error
}

func (f *fakeDB) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	f.calls++
	if len(args) > 0 {
		f.days = args[0]
	}
	return fakeRow{payload: f.payload, err: f.err}
}

type fakeRow struct {
	payload []byte
	err     error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*[]byte)) = append([]byte(nil), r.payload...)
	return nil
}

func TestDashboardPeriodValidationAndDefault(t *testing.T) {
	db := &fakeDB{payload: []byte(`{"period_days":30}`)}
	h := NewHandler(db)
	for _, invalid := range []string{"0", "14", "91", "abc"} {
		rec := httptest.NewRecorder()
		h.GetDashboard(rec, httptest.NewRequest("GET", "/api/v1/internal/analytics/dashboard?days="+invalid, nil))
		if rec.Code != 400 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid days %q: status=%d headers=%v", invalid, rec.Code, rec.Header())
		}
	}
	rec := httptest.NewRecorder()
	h.GetDashboard(rec, httptest.NewRequest("GET", "/api/v1/internal/analytics/dashboard", nil))
	if rec.Code != 200 || db.days != 30 {
		t.Fatalf("default period status=%d days=%v", rec.Code, db.days)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store response header")
	}
}

func TestDashboardCacheIsBoundedToSupportedPeriods(t *testing.T) {
	db := &fakeDB{payload: []byte(`{"period_days":7}`)}
	h := NewHandler(db)
	h.now = func() time.Time { return time.Unix(100, 0) }
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.GetDashboard(rec, httptest.NewRequest("GET", "/api/v1/internal/analytics/dashboard?days=7", nil))
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if db.calls != 1 || len(h.cache) != 1 {
		t.Fatalf("calls=%d cache keys=%d", db.calls, len(h.cache))
	}
	if _, ok := h.cache[7]; !ok {
		t.Fatal("expected supported period in cache")
	}
}

func TestDashboardDatabaseFailureDoesNotCache(t *testing.T) {
	db := &fakeDB{err: errors.New("database unavailable")}
	h := NewHandler(db)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.GetDashboard(rec, httptest.NewRequest("GET", "/api/v1/internal/analytics/dashboard?days=90", nil))
		if rec.Code != 500 || !strings.Contains(rec.Body.String(), "Dashboard data unavailable") {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatal("error body is not JSON")
		}
	}
	if db.calls != 2 || len(h.cache) != 0 {
		t.Fatalf("failed query cached: calls=%d keys=%d", db.calls, len(h.cache))
	}
}

func TestDashboardQueryExposesRequestedFunnelMeasures(t *testing.T) {
	for _, field := range []string{
		"median_time_to_first_vote_seconds", "median_votes_to_match",
		"rooms_without_second_participant", "rooms_without_second_participant_denominator",
		"rooms_without_second_participant_rate", "ticket_transitions",
		"match_ticket_transition_rate", "invite_open_rate_denominator",
		"invite_join_denominator", "activation_denominator", "match_denominator",
		"ticket_transition_denominator", "match_ticket_ctr_denominator",
		"rooms_without_second_participant_rate_denominator", "second_room_7d_denominator",
		"second_room_30d_denominator",
	} {
		if !strings.Contains(dashboardQuery, field) {
			t.Errorf("dashboard SQL does not expose %q", field)
		}
	}
}

func TestDashboardDeadlineReturnsGatewayTimeout(t *testing.T) {
	db := &fakeDB{err: context.DeadlineExceeded}
	h := NewHandler(db)
	rec := httptest.NewRecorder()
	h.GetDashboard(rec, httptest.NewRequest("GET", "/api/v1/internal/analytics/dashboard?days=7", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s; want gateway timeout", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store response header")
	}
}

func TestDashboardQueryAgainstPostgres(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var payload []byte
	if err := db.QueryRow(ctx, dashboardQuery, 30).Scan(&payload); err != nil {
		t.Fatalf("execute dashboard SQL: %v", err)
	}
	var result struct {
		Summary map[string]any `json:"summary"`
		Daily   []any          `json:"daily"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("decode dashboard JSON: %v", err)
	}
	for _, field := range []string{"rooms_created", "rooms_invite_shared", "rooms_joined", "activated_rooms", "rooms_matched", "rooms_ticket_clicked", "ticket_transitions", "created_match_conversion", "match_event_open_ctr", "match_ticket_ctr", "match_ticket_transition_rate", "no_match_rate", "median_time_to_first_vote_seconds", "median_swipes_to_match", "rooms_without_second_participant", "rooms_without_second_participant_denominator", "rooms_without_second_participant_rate", "room_creation_rate_denominator", "invite_open_denominator", "invite_join_denominator", "activation_denominator", "match_denominator", "ticket_transition_denominator", "no_match_denominator", "second_room_7d_denominator", "second_room_30d_denominator"} {
		if _, ok := result.Summary[field]; !ok {
			t.Errorf("summary is missing %s", field)
		}
	}
}
