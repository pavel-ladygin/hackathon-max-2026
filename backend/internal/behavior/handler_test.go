package behavior

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

type fakeIngester struct {
	err  error
	user uuid.UUID
}

func (f *fakeIngester) Ingest(_ context.Context, user uuid.UUID, _ []ClientEvent) (Result, error) {
	f.user = user
	return Result{}, f.err
}

func behaviorTestRouter(service Ingester) http.Handler {
	h := NewHandler(service)
	return httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) { h.RegisterRoutes(r, func(next http.Handler) http.Handler { return next }) })
}

func TestIngestMapsInvalidServiceErrorToValidationAndUsesPrincipal(t *testing.T) {
	user := uuid.New()
	ingester := &fakeIngester{err: ErrInvalid}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/behavior/events:batch", strings.NewReader(`{"events":[{"client_event_id":"a","type":"open","occurred_at":"2026-09-20T10:00:00Z"}]}`))
	req = req.WithContext(contracts.WithPrincipal(req.Context(), contracts.Principal{UserID: user}))
	res := httptest.NewRecorder()
	behaviorTestRouter(ingester).ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"VALIDATION_FAILED"`) || ingester.user != user {
		t.Fatalf("status/user/body=%d/%s/%s", res.Code, ingester.user, res.Body.String())
	}

	ingester.err = errors.New("db")
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/behavior/events:batch", strings.NewReader(`{"events":[{"client_event_id":"a","type":"open","occurred_at":"2026-09-20T10:00:00Z"}]}`))
	req = req.WithContext(contracts.WithPrincipal(req.Context(), contracts.Principal{UserID: user}))
	behaviorTestRouter(ingester).ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestDecodeBatchStrictMetadataByType(t *testing.T) {
	valid := `{"events":[{"client_event_id":"a","type":"impression","occurred_at":"2026-09-20T10:00:00Z","metadata":{"surface":"feed","position":0,"request_id":"r"}},{"client_event_id":"b","type":"open","occurred_at":"2026-09-20T10:00:00Z","metadata":{"surface":"detail","request_id":"r"}},{"client_event_id":"c","type":"share","occurred_at":"2026-09-20T10:00:00Z","metadata":{"surface":"detail"}}]}`
	request := httptest.NewRequest("POST", "/", strings.NewReader(valid))
	if events, err := decodeBatch(httptest.NewRecorder(), request); err != nil || len(events) != 3 {
		t.Fatalf("valid batch events=%#v err=%v", events, err)
	}
	for _, body := range []string{
		`{"events":[{"client_event_id":"a","type":"like","occurred_at":"2026-09-20T10:00:00Z"}]}`,
		`{"events":[{"client_event_id":"a","type":"share","occurred_at":"2026-09-20T10:00:00Z","metadata":{"request_id":"r"}}]}`,
		`{"events":[{"client_event_id":"a","type":"open","occurred_at":"2026-09-20T10:00:00Z","metadata":{"position":1}}]}`,
		`{"events":[{"client_event_id":"a","type":"open","occurred_at":"2026-09-20T10:00:00Z","metadata":{"free_text":"x"}}]}`,
	} {
		request := httptest.NewRequest("POST", "/", strings.NewReader(body))
		if _, err := decodeBatch(httptest.NewRecorder(), request); err == nil {
			t.Fatalf("invalid batch accepted: %s", body)
		}
	}
}

func TestDecodeBatchAcceptsAnalyticsEnvelopeAndRejectsUnsafeOrServerEvents(t *testing.T) {
	userSession := uuid.NewString()
	valid := `{"events":[{"client_event_id":"evt-1","type":"onboarding_completed","event_version":1,"occurred_at":"2026-09-20T10:00:00Z","session_id":"` + userSession + `","platform":"max_android","app_version":"1.2.3","entry_point":"feed","properties":{"selected_categories_count":3,"budget_configured":true}}]}`
	events, err := decodeBatch(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(valid)))
	if err != nil || len(events) != 1 || events[0].EventVersion != 1 || !events[0].SessionID.Valid || events[0].Platform.String != "max_android" || len(events[0].Properties) == 0 {
		t.Fatalf("valid envelope events=%#v err=%v", events, err)
	}
	for _, body := range []string{
		`{"events":[{"client_event_id":"a","type":"search_performed","event_version":1,"occurred_at":"2026-09-20T10:00:00Z","properties":{"query":"raw private text"}}]}`,
		`{"events":[{"client_event_id":"a","type":"event_saved","event_version":1,"occurred_at":"2026-09-20T10:00:00Z"}]}`,
		`{"events":[{"client_event_id":"a","type":"onboarding_started","event_version":2,"occurred_at":"2026-09-20T10:00:00Z"}]}`,
	} {
		if _, err := decodeBatch(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body))); err == nil {
			t.Fatalf("invalid analytics event accepted: %s", body)
		}
	}
}

func TestDecodeBatchAllowsSafeFailureEventsOnlyWithBoundedCodes(t *testing.T) {
	valid := `{"events":[{"client_event_id":"failed-1","type":"room_join_failed","event_version":1,"occurred_at":"2026-09-20T10:00:00Z","room_id":"` + uuid.NewString() + `","properties":{"error_code":"not_found"}},{"client_event_id":"perf-1","type":"client_performance","event_version":1,"occurred_at":"2026-09-20T10:00:01Z","properties":{"operation":"feed_load","duration_ms":600000}}]}`
	if events, err := decodeBatch(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(valid))); err != nil || len(events) != 2 {
		t.Fatalf("valid failure/performance events=%#v err=%v", events, err)
	}
	for _, body := range []string{
		`{"events":[{"client_event_id":"a","type":"client_error","event_version":1,"occurred_at":"2026-09-20T10:00:00Z","properties":{"error_code":"private-message"}}]}`,
		`{"events":[{"client_event_id":"a","type":"client_performance","event_version":1,"occurred_at":"2026-09-20T10:00:00Z","properties":{"operation":"feed_load","duration_ms":600001}}]}`,
	} {
		if _, err := decodeBatch(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body))); err == nil {
			t.Fatalf("unsafe failure/performance event accepted: %s", body)
		}
	}
}
