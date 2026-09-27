package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

func testRouterService(t *testing.T) (*Service, string) {
	now := time.Unix(1_700_000_000, 0)
	repo := &fakeAuthRepo{user: platformUserForHandler()}
	s := testService(t, repo, now)
	values := validValues(now)
	values["auth_date"] = "1700000000"
	return s, signedInitData(t, "secret", values)
}

func platformUserForHandler() platform.User {
	return platform.User{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), DisplayName: "Ada Lovelace", Locale: "en", OnboardingState: "new"}
}

func TestBootstrapRouterValidResponseContainsRequiredNullFields(t *testing.T) {
	s, raw := testRouterService(t)
	router := httpapi.NewRouter(nil, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), s.RegisterRoutes)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/max/bootstrap", strings.NewReader(`{"init_data":"`+raw+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["token_type"] != "Bearer" || body["access_token"] == "" {
		t.Fatalf("unexpected token response: %s", res.Body.String())
	}
	if value, ok := body["preferences"]; !ok || value != nil {
		t.Fatalf("preferences must be explicit null: %#v", value)
	}
	if value, ok := body["invite_context"]; !ok || value != nil {
		t.Fatalf("invite_context must be explicit null: %#v", value)
	}
	if value, ok := body["shared_event_id"]; !ok || value != nil {
		t.Fatalf("shared_event_id must be explicit null: %#v", value)
	}
}

func TestBootstrapReturnsPersistedPreferences(t *testing.T) {
	s, raw := testRouterService(t)
	repo := s.repo.(*fakeAuthRepo)
	repo.user.OnboardingState = "complete"
	repo.preferences = &preferences.Value{
		CityID: uuid.MustParse("22222222-2222-4222-8222-222222222222"), InterestSlugs: []string{"concerts", "food"},
		BudgetMaxMinor: 350000, UsualDayTypes: []string{}, UsualTimeSlots: []string{"evening"},
		Version: 2, UpdatedAt: time.Unix(1_700_000_123, 0).UTC(),
	}
	repo.dailyNotificationsEnabled = true
	router := httpapi.NewRouter(nil, slog.Default(), s.RegisterRoutes)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/max/bootstrap", strings.NewReader(`{"init_data":"`+raw+`"}`))
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		OnboardingState           string                      `json:"onboarding_state"`
		DailyNotificationsEnabled bool                        `json:"daily_notifications_enabled"`
		Preferences               *preferencesResponseForTest `json:"preferences"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OnboardingState != "complete" || !body.DailyNotificationsEnabled || body.Preferences == nil || body.Preferences.Version != 2 || body.Preferences.CityID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("bootstrap preferences mismatch: %s", res.Body.String())
	}
	if len(body.Preferences.InterestSlugs) != 2 || len(body.Preferences.UsualDayTypes) != 0 || len(body.Preferences.UsualTimeSlots) != 1 {
		t.Fatalf("bootstrap preference arrays mismatch: %s", res.Body.String())
	}
}

type preferencesResponseForTest struct {
	CityID         string   `json:"city_id"`
	InterestSlugs  []string `json:"interest_slugs"`
	UsualDayTypes  []string `json:"usual_day_types"`
	UsualTimeSlots []string `json:"usual_time_slots"`
	Version        int      `json:"version"`
}

func TestBootstrapRejectsUnknownTrailingAndMismatchedHintWithCanonicalRequestID(t *testing.T) {
	s, raw := testRouterService(t)
	router := httpapi.NewRouter(nil, slog.Default(), s.RegisterRoutes)
	for name, payload := range map[string]string{
		"unknown field":   `{"init_data":"` + raw + `","extra":true}`,
		"trailing json":   `{"init_data":"` + raw + `"}{"second":true}`,
		"mismatched hint": `{"init_data":"` + raw + `","start_param":"wrong"}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/max/bootstrap", strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			var envelope struct {
				Error struct {
					Code      string `json:"code"`
					Message   string `json:"message"`
					RequestID string `json:"request_id"`
				} `json:"error"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != "VALIDATION_FAILED" || envelope.Error.Message != "Invalid bootstrap request" || envelope.Error.RequestID == "" {
				t.Fatalf("unsafe/noncanonical envelope: %s", res.Body.String())
			}
			if res.Header().Get("X-Request-ID") != envelope.Error.RequestID {
				t.Fatalf("request ID header/body mismatch")
			}
		})
	}
}

func TestBootstrapRepositoryFailureUsesSafeInternalEnvelope(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repo := &fakeAuthRepo{user: platformUserForHandler(), bootstrapErr: errors.New("database password leaked")}
	s := testService(t, repo, now)
	values := validValues(now)
	values["auth_date"] = "1700000000"
	router := httpapi.NewRouter(nil, slog.Default(), s.RegisterRoutes)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/max/bootstrap", strings.NewReader(`{"init_data":"`+signedInitData(t, "secret", values)+`"}`))
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || strings.Contains(res.Body.String(), "database password") || !strings.Contains(res.Body.String(), `"code":"INTERNAL"`) {
		t.Fatalf("unsafe repository error response: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAuthMiddlewareRequiresSingleBearerAndSharesContractsPrincipal(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	token := validOpaqueToken()
	id := uuid.New()
	h := sha256.Sum256([]byte(token))
	repo := &fakeAuthRepo{byHash: map[[32]byte]platform.GetAuthSessionRow{h: {UserID: id, ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}}}
	s := testService(t, repo, now)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := contracts.PrincipalFromContext(r.Context())
		if !ok || p.UserID != id {
			t.Fatalf("principal missing: %+v %v", p, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	for name, headers := range map[string][]string{
		"missing": nil, "multiple": {"Bearer " + token, "Bearer " + token}, "wrong scheme": {"Basic " + token}, "malformed": {"Bearer"},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			for _, value := range headers {
				req.Header.Add("Authorization", value)
			}
			res := httptest.NewRecorder()
			s.Middleware(next).ServeHTTP(res, req)
			if res.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d", res.Code)
			}
		})
	}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "bearer "+token)
	res := httptest.NewRecorder()
	s.Middleware(next).ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("valid bearer status=%d", res.Code)
	}
}

func TestBootstrapRateLimitIsPerPeerAndResetsWithoutTrustingForwardedHeaders(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	validator, err := newMAXValidator("secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{validator: validator, repo: &fakeAuthRepo{}, now: func() time.Time { return now }}
	router := httpapi.NewRouter(nil, slog.Default(), s.RegisterRoutes)
	request := func(peer, forwarded string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/max/bootstrap", strings.NewReader(`{"init_data":"bad"}`))
		req.RemoteAddr = peer
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	for i := 0; i < 20; i++ {
		if got := request("192.0.2.10:4000", "").Code; got != http.StatusUnauthorized {
			t.Fatalf("request %d status=%d", i+1, got)
		}
	}
	limited := request("192.0.2.10:4000", "203.0.113.7")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit response: status=%d headers=%v", limited.Code, limited.Header())
	}
	if got := request("192.0.2.11:4000", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("different peer status=%d", got)
	}
	now = now.Add(time.Minute + time.Second)
	if got := request("192.0.2.10:4000", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("reset peer status=%d", got)
	}
}
