package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
