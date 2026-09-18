package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/auth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
)

func TestPreferencesPostgresReplaceGetAndIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	cityA, cityB := uuid.New(), uuid.New()
	userA, userB := uuid.New(), uuid.New()
	for _, city := range []uuid.UUID{cityA, cityB} {
		if _, err := db.Exec(ctx, "INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,$2,'UTC',0,0)", city, "preferences-"+city.String()); err != nil {
			t.Fatal("insert city:", err)
		}
	}
	for i, user := range []uuid.UUID{userA, userB} {
		if _, err := db.Exec(ctx, "INSERT INTO users (id,max_user_id,display_name,locale) VALUES ($1,$2,$3,'ru-RU')", user, time.Now().UnixNano()+int64(i), "preferences-"+user.String()); err != nil {
			t.Fatal("insert user:", err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, "DELETE FROM users WHERE id IN ($1,$2)", userA, userB)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM cities WHERE id IN ($1,$2)", cityA, cityB)
	})

	service := preferences.NewService(db)
	if got, found, err := service.Get(ctx, userA); err != nil || found || got.Version != 0 || got.CityID != uuid.Nil {
		t.Fatalf("Get before PUT = %#v, found=%v, err=%v; want empty, false, nil", got, found, err)
	}

	first, err := service.Replace(ctx, userA, preferences.Input{
		CityID: cityA, InterestSlugs: []string{"concerts", "food"}, BudgetMaxMinor: 250000,
		UsualDayTypes: []string{"weekday", "weekend"}, UsualTimeSlots: []string{"evening"},
	})
	if err != nil {
		t.Fatalf("first Replace: %v", err)
	}
	if first.Version != 1 || first.CityID != cityA || first.BudgetMaxMinor != 250000 {
		t.Fatalf("first value = %#v; want city/budget/version preserved", first)
	}
	if _, err := db.Exec(ctx, "UPDATE users SET onboarding_state='new' WHERE id=$1", userB); err != nil {
		t.Fatal("reset user B:", err)
	}

	second, err := service.Replace(ctx, userA, preferences.Input{
		CityID: cityB, InterestSlugs: []string{"sports"}, BudgetMaxMinor: 0,
		UsualDayTypes: []string{}, UsualTimeSlots: []string{},
	})
	if err != nil {
		t.Fatalf("replacement with empty day/time arrays: %v", err)
	}
	if second.Version != first.Version+1 || second.CityID != cityB || len(second.UsualDayTypes) != 0 || len(second.UsualTimeSlots) != 0 {
		t.Fatalf("second value = %#v; want full replacement and incremented version", second)
	}
	var categories []string
	if err := db.QueryRow(ctx, "SELECT COALESCE(array_agg(category_slug ORDER BY category_slug), ARRAY[]::text[]) FROM user_category_preferences WHERE user_id=$1", userA).Scan(&categories); err != nil {
		t.Fatal("read replacement categories:", err)
	}
	if len(categories) != 1 || categories[0] != "sports" {
		t.Fatalf("stored categories = %#v; old categories were not removed", categories)
	}
	var state string
	if err := db.QueryRow(ctx, "SELECT onboarding_state FROM users WHERE id=$1", userA).Scan(&state); err != nil || state != "complete" {
		t.Fatalf("onboarding state = %q, err=%v; want complete", state, err)
	}

	other, err := service.Replace(ctx, userB, preferences.Input{CityID: cityA, InterestSlugs: []string{"theatre"}, BudgetMaxMinor: 100, UsualDayTypes: nil, UsualTimeSlots: nil})
	if err != nil || other.Version != 1 {
		t.Fatalf("user B Replace = %#v, err=%v; want independent version 1", other, err)
	}
	gotA, foundA, err := service.Get(ctx, userA)
	if err != nil || !foundA || len(gotA.InterestSlugs) != 1 || gotA.InterestSlugs[0] != "sports" {
		t.Fatalf("user A after user B update = %#v, found=%v, err=%v; isolation broken", gotA, foundA, err)
	}
}

func TestPreferencesRejectInvalidInputAndUnknownCity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	city, user := uuid.New(), uuid.New()
	if _, err := db.Exec(ctx, "INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,'preferences-validation','UTC',0,0)", city); err != nil {
		t.Fatal("insert city:", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO users (id,max_user_id,display_name,locale) VALUES ($1,$2,'preferences-validation','ru-RU')", user, time.Now().UnixNano()); err != nil {
		t.Fatal("insert user:", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, "DELETE FROM users WHERE id=$1", user)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM cities WHERE id=$1", city)
	})
	service := preferences.NewService(db)
	valid := preferences.Input{CityID: city, InterestSlugs: []string{"food"}, BudgetMaxMinor: 10, UsualDayTypes: nil, UsualTimeSlots: nil}
	cases := []struct {
		name   string
		mutate func(*preferences.Input)
	}{
		{"unknown city", func(in *preferences.Input) { in.CityID = uuid.New() }},
		{"invalid category", func(in *preferences.Input) { in.InterestSlugs = []string{"not-a-category"} }},
		{"duplicate category", func(in *preferences.Input) { in.InterestSlugs = []string{"food", "food"} }},
		{"invalid day type", func(in *preferences.Input) { in.UsualDayTypes = []string{"holiday"} }},
		{"invalid time slot", func(in *preferences.Input) { in.UsualTimeSlots = []string{"lunchtime"} }},
		{"negative budget", func(in *preferences.Input) { in.BudgetMaxMinor = -1 }},
		{"budget too large", func(in *preferences.Input) { in.BudgetMaxMinor = 100000001 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := valid
			tc.mutate(&input)
			if _, err := service.Replace(ctx, user, input); !errors.Is(err, preferences.ErrInvalid) {
				t.Fatalf("Replace error = %v; want ErrInvalid", err)
			}
		})
	}
	if _, found, err := service.Get(ctx, user); err != nil || found {
		t.Fatalf("invalid replacements persisted preferences: found=%v err=%v", found, err)
	}
}

func TestPreferencesHTTPRequiresAuthAndRejectsMissingFields(t *testing.T) {
	db := openTestDB(t)
	authService, err := auth.NewService(db, integrationBotToken, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service := preferences.NewService(db)
	handler := preferences.NewHandler(service)
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) {
		handler.RegisterRoutes(r, authService.Middleware)
	})

	unauthenticated := httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences", bytes.NewBufferString(`{}`))
	unauthenticated.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, unauthenticated)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("without auth status = %d; want 401", res.Code)
	}

	userID, cityID, token := uuid.New(), uuid.New(), opaqueSessionToken(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,'preferences-http','UTC',0,0)", cityID); err != nil {
		t.Fatal("insert city:", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO users (id,max_user_id,display_name,locale) VALUES ($1,$2,'preferences-http','ru-RU')", userID, time.Now().UnixNano()); err != nil {
		t.Fatal("insert user:", err)
	}
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, "INSERT INTO auth_sessions (id,user_id,token_hash,expires_at) VALUES ($1,$2,$3,now()+interval '1 hour')", uuid.New(), userID, hash[:]); err != nil {
		t.Fatal("insert session:", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, "DELETE FROM users WHERE id=$1", userID)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM cities WHERE id=$1", cityID)
	})

	missing := httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences", bytes.NewBufferString(`{"budget_max_minor":100}`))
	missing.Header.Set("Content-Type", "application/json")
	missing.Header.Set("Authorization", "Bearer "+token)
	res = httptest.NewRecorder()
	router.ServeHTTP(res, missing)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("missing fields status = %d; want 400", res.Code)
	}

	valid := httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences", bytes.NewBufferString(`{"city_id":"`+cityID.String()+`","interest_slugs":["cinema"],"budget_max_minor":900,"usual_day_types":[],"usual_time_slots":[]}`))
	valid.Header.Set("Content-Type", "application/json")
	valid.Header.Set("Authorization", "Bearer "+token)
	res = httptest.NewRecorder()
	router.ServeHTTP(res, valid)
	if res.Code != http.StatusOK {
		t.Fatalf("valid PUT status = %d, body=%s; want 200", res.Code, res.Body.String())
	}
	if got, found, err := service.Get(ctx, userID); err != nil || !found || got.BudgetMaxMinor != 900 || len(got.InterestSlugs) != 1 || got.InterestSlugs[0] != "cinema" {
		t.Fatalf("valid PUT persistence = %#v, found=%v, err=%v", got, found, err)
	}
}

func opaqueSessionToken(t *testing.T) string {
	t.Helper()
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
