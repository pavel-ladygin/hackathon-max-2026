package saved

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

type fakeProvider struct {
	setFn       func(context.Context, uuid.UUID, uuid.UUID, bool) (State, error)
	listFn      func(context.Context, uuid.UUID, ListInput) (Page, error)
	encoded     string
	decoded     Cursor
	decodeCalls int
	setCalls    int
	lastUser    uuid.UUID
	lastEvent   uuid.UUID
	lastSaved   bool
	lastInput   ListInput
}

func (f *fakeProvider) Set(ctx context.Context, user, event uuid.UUID, saved bool) (State, error) {
	f.setCalls++
	f.lastUser, f.lastEvent, f.lastSaved = user, event, saved
	if f.setFn != nil {
		return f.setFn(ctx, user, event, saved)
	}
	return State{EventID: event, Saved: saved}, nil
}
func (f *fakeProvider) List(ctx context.Context, user uuid.UUID, input ListInput) (Page, error) {
	f.lastUser, f.lastInput = user, input
	if f.listFn != nil {
		return f.listFn(ctx, user, input)
	}
	return Page{}, nil
}
func (f *fakeProvider) EncodeCursor(uuid.UUID, Tab, Cursor) (string, error) { return f.encoded, nil }
func (f *fakeProvider) DecodeCursor(uuid.UUID, Tab, string) (Cursor, error) {
	f.decodeCalls++
	return f.decoded, nil
}

func savedTestRouter(provider Provider) http.Handler {
	h := NewHandler(provider)
	return httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) {
		h.RegisterRoutes(r, func(next http.Handler) http.Handler { return next })
	})
}

func withSavedPrincipal(req *http.Request, user uuid.UUID) *http.Request {
	return req.WithContext(contracts.WithPrincipal(req.Context(), contracts.Principal{UserID: user}))
}

func TestSetHandlerRequiresPrincipalAndStrictJSON(t *testing.T) {
	user, event := uuid.New(), uuid.New()
	provider := &fakeProvider{}
	h := savedTestRouter(provider)

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/api/v1/me/saved-events/"+event.String(), strings.NewReader(`{"saved":true}`)))
	if res.Code != http.StatusUnauthorized || provider.setCalls != 0 || res.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("missing principal: status=%d calls=%d headers=%v body=%s", res.Code, provider.setCalls, res.Header(), res.Body.String())
	}

	for _, body := range []string{`{}`, `{"saved":true,"extra":1}`, `{"saved":"true"}`, `{"saved":null}`, `{"saved":true} trailing`} {
		t.Run(body, func(t *testing.T) {
			res := httptest.NewRecorder()
			req := withSavedPrincipal(httptest.NewRequest(http.MethodPut, "/api/v1/me/saved-events/"+event.String(), strings.NewReader(body)), user)
			h.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s; want 400", res.Code, res.Body.String())
			}
		})
	}
	if provider.setCalls != 0 {
		t.Fatalf("strict decode invoked provider %d times", provider.setCalls)
	}
}

func TestSetHandlerPropagatesPrincipalAndNullableState(t *testing.T) {
	user, event := uuid.New(), uuid.New()
	savedAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	provider := &fakeProvider{setFn: func(_ context.Context, gotUser, gotEvent uuid.UUID, saved bool) (State, error) {
		if !saved {
			return State{EventID: gotEvent, Saved: false}, nil
		}
		return State{EventID: gotEvent, Saved: true, SavedAt: &savedAt}, nil
	}}
	h := savedTestRouter(provider)
	res := httptest.NewRecorder()
	req := withSavedPrincipal(httptest.NewRequest(http.MethodPut, "/api/v1/me/saved-events/"+event.String(), strings.NewReader(`{"saved":true}`)), user)
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK || provider.lastUser != user || provider.lastEvent != event || !provider.lastSaved {
		t.Fatalf("status=%d provider=%#v body=%s", res.Code, provider, res.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["event_id"] != event.String() || body["saved"] != true || body["saved_at"] != savedAt.Format(time.RFC3339) {
		t.Fatalf("response = %#v", body)
	}

	provider.setFn = func(_ context.Context, _, gotEvent uuid.UUID, _ bool) (State, error) {
		return State{EventID: gotEvent, Saved: false}, nil
	}
	res = httptest.NewRecorder()
	req = withSavedPrincipal(httptest.NewRequest(http.MethodPut, "/api/v1/me/saved-events/"+event.String(), strings.NewReader(`{"saved":false}`)), user)
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unsave status=%d body=%s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["saved"] != false || body["saved_at"] != nil {
		t.Fatalf("unsave response = %#v; want null saved_at", body)
	}
}

func TestSetHandlerMapsInvalidAndUnknownEventsToCanonicalNotFound(t *testing.T) {
	user := uuid.New()
	provider := &fakeProvider{setFn: func(context.Context, uuid.UUID, uuid.UUID, bool) (State, error) { return State{}, ErrNotFound }}
	h := savedTestRouter(provider)
	for _, path := range []string{"not-a-uuid", uuid.Nil.String(), uuid.NewString()} {
		res := httptest.NewRecorder()
		req := withSavedPrincipal(httptest.NewRequest(http.MethodPut, "/api/v1/me/saved-events/"+path, strings.NewReader(`{"saved":true}`)), user)
		h.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"code":"NOT_FOUND"`) {
			t.Fatalf("path=%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
}

func TestListHandlerDefaultsValidatesQueryAndMapsCursorAndNullables(t *testing.T) {
	user, event, cursorID := uuid.New(), uuid.New(), uuid.New()
	savedAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	provider := &fakeProvider{encoded: "next-token", decoded: Cursor{At: savedAt, ID: cursorID}, listFn: func(_ context.Context, gotUser uuid.UUID, input ListInput) (Page, error) {
		if gotUser != user || input.Tab != TabSaved || input.Limit != 2 || input.Cursor == nil {
			return Page{}, errors.New("unexpected list input")
		}
		return Page{Items: []Item{{Event: Card{ID: event, Title: "Event", CategorySlug: "concerts", StartsAt: savedAt, Timezone: "UTC", VenueName: "Venue", Currency: "RUB", PriceLabel: "Цена уточняется"}, SavedAt: nil}}, NextCursor: &Cursor{At: savedAt, ID: cursorID}}, nil
	}}
	h := savedTestRouter(provider)
	res := httptest.NewRecorder()
	req := withSavedPrincipal(httptest.NewRequest(http.MethodGet, "/api/v1/me/saved-events?limit=2&cursor=old-token", nil), user)
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK || provider.decodeCalls != 1 || provider.lastInput.Tab != TabSaved || provider.lastInput.Limit != 2 {
		t.Fatalf("status=%d calls=%d input=%#v body=%s", res.Code, provider.decodeCalls, provider.lastInput, res.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	items := body["items"].([]any)
	item := items[0].(map[string]any)
	if item["saved_at"] != nil || item["match"] != nil || body["next_cursor"] != "next-token" {
		t.Fatalf("response = %#v; want nullable saved_at/match and cursor", body)
	}
	defaultProvider := &fakeProvider{listFn: func(_ context.Context, gotUser uuid.UUID, input ListInput) (Page, error) {
		if gotUser != user || input.Tab != TabSaved || input.Limit != defaultLimit || input.Cursor != nil {
			return Page{}, errors.New("unexpected default list input")
		}
		return Page{}, nil
	}}
	res = httptest.NewRecorder()
	req = withSavedPrincipal(httptest.NewRequest(http.MethodGet, "/api/v1/me/saved-events", nil), user)
	savedTestRouter(defaultProvider).ServeHTTP(res, req)
	if res.Code != http.StatusOK || defaultProvider.decodeCalls != 0 {
		t.Fatalf("default query status=%d decode_calls=%d body=%s", res.Code, defaultProvider.decodeCalls, res.Body.String())
	}

	for _, query := range []string{"?limit=0", "?limit=51", "?tab=bad", "?tab=saved&tab=matches"} {
		res := httptest.NewRecorder()
		req := withSavedPrincipal(httptest.NewRequest(http.MethodGet, "/api/v1/me/saved-events"+query, nil), user)
		h.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"VALIDATION_FAILED"`) {
			t.Fatalf("query=%s status=%d body=%s", query, res.Code, res.Body.String())
		}
	}
}
