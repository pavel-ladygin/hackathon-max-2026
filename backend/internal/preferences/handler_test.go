package preferences

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

type fakeReplacer struct {
	input  Input
	userID uuid.UUID
	value  Value
	err    error
	calls  int
}

func (f *fakeReplacer) Replace(_ context.Context, userID uuid.UUID, input Input) (Value, error) {
	f.calls++
	f.userID = userID
	f.input = input
	return f.value, f.err
}

func TestReplaceMapsRequestToServiceAndResponse(t *testing.T) {
	userID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	cityID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	updatedAt := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	fake := &fakeReplacer{value: Value{
		CityID: cityID, InterestSlugs: []string{"concerts", "food"}, BudgetMaxMinor: 350000,
		UsualDayTypes: []string{"weekend"}, UsualTimeSlots: []string{"evening"}, Version: 3, UpdatedAt: updatedAt,
	}}
	handler := NewHandler(fake)
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) {
		handler.RegisterRoutes(r, injectPrincipal(userID))
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences", bytes.NewBufferString(`{"city_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","interest_slugs":["concerts","food"],"budget_max_minor":350000,"usual_day_types":["weekend"],"usual_time_slots":["evening"]}`))
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if fake.calls != 1 || fake.userID != userID || fake.input.CityID != cityID || fake.input.BudgetMaxMinor != 350000 {
		t.Fatalf("unexpected service call: %#v", fake)
	}
	var body struct {
		CityID         uuid.UUID `json:"city_id"`
		InterestSlugs  []string  `json:"interest_slugs"`
		BudgetMaxMinor int       `json:"budget_max_minor"`
		Version        int       `json:"version"`
		UpdatedAt      time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CityID != cityID || body.BudgetMaxMinor != 350000 || body.Version != 3 || !body.UpdatedAt.Equal(updatedAt) || len(body.InterestSlugs) != 2 {
		t.Fatalf("unexpected response: %s", res.Body.String())
	}
}

func TestReplaceRejectsMalformedAndInvalidContractBodies(t *testing.T) {
	valid := `{"city_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","interest_slugs":["concerts"],"budget_max_minor":0,"usual_day_types":[],"usual_time_slots":[]}`
	for name, body := range map[string]string{
		"unknown field":    valid[:len(valid)-1] + `,"extra":true}`,
		"trailing JSON":    valid + `{}`,
		"missing field":    `{"city_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","interest_slugs":["concerts"],"budget_max_minor":0,"usual_day_types":[]}`,
		"null array":       `{"city_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","interest_slugs":null,"budget_max_minor":0,"usual_day_types":[],"usual_time_slots":[]}`,
		"duplicate values": `{"city_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","interest_slugs":["concerts","concerts"],"budget_max_minor":0,"usual_day_types":[],"usual_time_slots":[]}`,
		"unknown category": `{"city_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","interest_slugs":["not-a-category"],"budget_max_minor":0,"usual_day_types":[],"usual_time_slots":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeReplacer{}
			handler := NewHandler(fake)
			router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) {
				handler.RegisterRoutes(r, injectPrincipal(uuid.New()))
			})
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences", bytes.NewBufferString(body)))
			if res.Code != http.StatusBadRequest || fake.calls != 0 || !hasErrorCode(t, res, "VALIDATION_FAILED") {
				t.Fatalf("status=%d calls=%d body=%s", res.Code, fake.calls, res.Body.String())
			}
		})
	}
}

func TestReplaceProtectsPrincipalAndMapsServiceFailures(t *testing.T) {
	t.Run("missing principal", func(t *testing.T) {
		fake := &fakeReplacer{}
		handler := NewHandler(fake)
		router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) { r.Put("/api/v1/me/preferences", handler.Replace) })
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences", bytes.NewBufferString(validPreferencesJSON())))
		if res.Code != http.StatusUnauthorized || fake.calls != 0 || !hasErrorCode(t, res, "UNAUTHENTICATED") || res.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("status=%d calls=%d headers=%v body=%s", res.Code, fake.calls, res.Header(), res.Body.String())
		}
	})
	t.Run("domain validation", func(t *testing.T) {
		fake := &fakeReplacer{err: ErrInvalid}
		res := replaceAsPrincipal(t, NewHandler(fake), validPreferencesJSON())
		if res.Code != http.StatusBadRequest || !hasErrorCode(t, res, "VALIDATION_FAILED") {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})
	t.Run("internal error remains safe", func(t *testing.T) {
		fake := &fakeReplacer{err: errors.New("database password")}
		res := replaceAsPrincipal(t, NewHandler(fake), validPreferencesJSON())
		if res.Code != http.StatusInternalServerError || !hasErrorCode(t, res, "INTERNAL") || bytes.Contains(res.Body.Bytes(), []byte("database password")) {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
	})
}

func replaceAsPrincipal(t *testing.T, handler *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) { handler.RegisterRoutes(r, injectPrincipal(uuid.New())) })
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences", bytes.NewBufferString(body)))
	return res
}

func injectPrincipal(userID uuid.UUID) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: userID})))
		})
	}
}

func validPreferencesJSON() string {
	return `{"city_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","interest_slugs":["concerts"],"budget_max_minor":0,"usual_day_types":[],"usual_time_slots":[]}`
}

func hasErrorCode(t *testing.T, res *httptest.ResponseRecorder, want string) bool {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(res.Body.Bytes(), &body) == nil && body.Error.Code == want
}
