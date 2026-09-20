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
