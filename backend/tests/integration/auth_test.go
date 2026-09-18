package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/auth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
)

const integrationBotToken = "a1-integration-test-bot-not-a-production-secret"

func signedMAXFixture(t *testing.T, maxID int64, name string) string {
	t.Helper()
	user, err := json.Marshal(map[string]any{"id": maxID, "first_name": name, "last_name": "Тест", "language_code": "ru", "photo_url": nil, "ignored_private_field": "never-persist-this"})
	if err != nil {
		t.Fatal("marshal fixture")
	}
	date := strconv.FormatInt(time.Now().Unix(), 10)
	check := "auth_date=" + date + "\nuser=" + string(user)
	key := hmac.New(sha256.New, []byte("WebAppData"))
	key.Write([]byte(integrationBotToken))
	mac := hmac.New(sha256.New, key.Sum(nil))
	mac.Write([]byte(check))
	return "user=" + url.PathEscape(string(user)) + "&hash=" + hex.EncodeToString(mac.Sum(nil)) + "&auth_date=" + date
}

func TestAuthPostgresBootstrapAndSessions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	maxID := time.Now().UnixNano()
	cityID := uuid.New()
	if _, err := db.Exec(ctx, "INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,'auth-preferences','UTC',0,0)", cityID); err != nil {
		t.Fatal("insert city:", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.Exec(cleanupCtx, "DELETE FROM users WHERE max_user_id = $1", maxID); err != nil {
			t.Error("clean test user:", err)
		}
		if _, err := db.Exec(cleanupCtx, "DELETE FROM cities WHERE id = $1", cityID); err != nil {
			t.Error("clean test city:", err)
		}
	})
	service, err := auth.NewService(db, integrationBotToken, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	router := httpapi.NewRouter(nil, slog.New(slog.NewJSONHandler(&logs, nil)), service.RegisterRoutes, func(r chi.Router) {
		r.With(service.Middleware).Get("/test/principal", func(w http.ResponseWriter, r *http.Request) {
			principal, ok := contracts.PrincipalFromContext(r.Context())
			if !ok {
				http.Error(w, "principal missing", 500)
				return
			}
			httpapi.WriteJSON(w, http.StatusOK, principal.UserID)
		})
	})
	bootstrap := func(raw string) (*httptest.ResponseRecorder, api.BootstrapResponse) {
		body, _ := json.Marshal(map[string]string{"init_data": raw})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/max/bootstrap", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		var response api.BootstrapResponse
		_ = json.Unmarshal(res.Body.Bytes(), &response)
		return res, response
	}

	raw := signedMAXFixture(t, maxID, "Иван + %20 ' ;")
	res, first := bootstrap(raw)
	if res.Code != 200 {
		t.Fatalf("bootstrap status = %d", res.Code)
	}
	if first.User.Id == uuid.Nil || first.User.DisplayName != "Иван + %20 ' ; Тест" || first.User.Locale != "ru-RU" || first.OnboardingState != "new" || first.ExpiresIn != 86400 || first.TokenType != "Bearer" {
		t.Fatal("bootstrap response fields invalid")
	}
	if !first.Preferences.IsNull() || !first.InviteContext.IsNull() {
		t.Fatal("A1 optional domain contexts must be null")
	}
	if res.Header().Get("Cache-Control") != "no-store" || res.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing response safety headers")
	}
	for _, private := range []string{"max_user_id", "never-persist-this", raw} {
		if strings.Contains(res.Body.String(), private) {
			t.Fatal("bootstrap leaked private identity")
		}
	}

	// Re-bootstrap updates MAX claims while returning the atomically persisted app profile.
	if _, err := preferences.NewService(db).Replace(ctx, first.User.Id, preferences.Input{
		CityID: cityID, InterestSlugs: []string{"concerts", "food"}, BudgetMaxMinor: 350000,
		UsualDayTypes: []string{"weekend"}, UsualTimeSlots: []string{"evening"},
	}); err != nil {
		t.Fatal(err)
	}
	res, second := bootstrap(signedMAXFixture(t, maxID, "Updated"))
	if res.Code != 200 || second.User.Id != first.User.Id || second.User.DisplayName != "Updated Тест" || second.OnboardingState != "complete" || first.AccessToken == second.AccessToken {
		t.Fatal("repeat bootstrap did not preserve identity/state or issue independent token")
	}
	saved, err := second.Preferences.Get()
	if err != nil || saved.CityId != cityID || saved.Version != 1 || saved.BudgetMaxMinor != 350000 || len(saved.InterestSlugs) != 2 {
		t.Fatalf("repeat bootstrap preferences = %#v, err=%v", saved, err)
	}

	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM users WHERE max_user_id=$1", maxID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("user count = %d, err=%v", count, err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM auth_sessions WHERE user_id=$1", first.User.Id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("session count = %d, err=%v", count, err)
	}
	for _, token := range []string{first.AccessToken, second.AccessToken} {
		hash := sha256.Sum256([]byte(token))
		var stored []byte
		var expires, created time.Time
		if err := db.QueryRow(ctx, "SELECT token_hash, expires_at, created_at FROM auth_sessions WHERE token_hash=$1", hash[:]).Scan(&stored, &expires, &created); err != nil {
			t.Fatal("hash lookup failed:", err)
		}
		if len(stored) != 32 || bytes.Equal(stored, []byte(token)) || !bytes.Equal(stored, hash[:]) {
			t.Fatal("session persistence must contain only SHA-256 hash")
		}
		if delta := expires.Sub(created); delta < 24*time.Hour-time.Minute || delta > 24*time.Hour+time.Minute {
			t.Fatal("session TTL must be 24h")
		}
		principal, err := service.Authenticate(ctx, token)
		if err != nil || principal.UserID != first.User.Id {
			t.Fatal("valid session did not resolve internal principal")
		}
		var record string
		if err := db.QueryRow(ctx, "SELECT row_to_json(s)::text FROM auth_sessions s WHERE token_hash=$1", hash[:]).Scan(&record); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(record, token) || strings.Contains(record, raw) {
			t.Fatal("sensitive credentials persisted")
		}
	}
	var userRecord string
	if err := db.QueryRow(ctx, "SELECT row_to_json(u)::text FROM users u WHERE id=$1", first.User.Id).Scan(&userRecord); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(userRecord, "never-persist-this") || strings.Contains(userRecord, raw) {
		t.Fatal("arbitrary MAX data persisted")
	}

	principalRequest := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/test/principal", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	res = principalRequest(first.AccessToken)
	var userID uuid.UUID
	if err := json.Unmarshal(res.Body.Bytes(), &userID); err != nil || res.Code != 200 || userID != first.User.Id {
		t.Fatal("middleware principal mismatch")
	}
	firstHash := sha256.Sum256([]byte(first.AccessToken))
	if _, err := db.Exec(ctx, "UPDATE auth_sessions SET expires_at=now()-interval '1 second' WHERE token_hash=$1", firstHash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, first.AccessToken); !errors.Is(err, auth.ErrTokenExpired) {
		t.Fatal("expired session accepted or wrong error")
	}
	res = principalRequest(first.AccessToken)
	if res.Code != 401 || !strings.Contains(res.Body.String(), "TOKEN_EXPIRED") {
		t.Fatal("expired session HTTP semantics")
	}
	secondHash := sha256.Sum256([]byte(second.AccessToken))
	if _, err := db.Exec(ctx, "UPDATE auth_sessions SET revoked_at=now() WHERE token_hash=$1", secondHash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, second.AccessToken); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("revoked session accepted")
	}
	if res := principalRequest(second.AccessToken); res.Code != 401 {
		t.Fatal("revoked bearer accepted by middleware")
	}
	if res := principalRequest("invalid"); res.Code != 401 {
		t.Fatal("invalid bearer accepted")
	}
	res, _ = bootstrap(raw + "&hash=duplicate")
	if res.Code != 401 {
		t.Fatal("invalid signed data accepted")
	}

	// Concurrent ON CONFLICT updates must retain one internal user, with independent sessions.
	const parallel = 6
	var wg sync.WaitGroup
	failures := make(chan error, parallel)
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, result := bootstrap(raw)
			if res.Code != 200 || result.User.Id != first.User.Id {
				failures <- fmt.Errorf("concurrent bootstrap status=%d, identity matches=%v", res.Code, result.User.Id == first.User.Id)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM auth_sessions WHERE user_id=$1", first.User.Id).Scan(&count); err != nil || count != 2+parallel {
		t.Fatalf("concurrent sessions count = %d, err=%v", count, err)
	}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM users WHERE max_user_id=$1", maxID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent user count = %d, err=%v", count, err)
	}
	for _, secret := range []string{raw, first.AccessToken, second.AccessToken, integrationBotToken} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("credentials leaked into logs")
		}
	}
}
