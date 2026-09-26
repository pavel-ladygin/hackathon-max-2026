package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

var serverTestMaxUserID int64 = time.Now().UnixNano()

func TestNewHandlerCreateRoomUsesProductionRecorderAndAuth(t *testing.T) {
	db := openServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	city, user, token := newServerRoomFixture(t, ctx, db)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h, err := newHandler(context.Background(), serverTestConfig(), db, logger)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"city_id":"` + city.String() + `","name":"HTTP room"}`
	unauthenticated := httptest.NewRecorder()
	h.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(body)))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s; want 401", unauthenticated.Code, unauthenticated.Body.String())
	}

	request := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", "http-create-replay")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := request(body)
	if first.Code != http.StatusCreated || first.Header().Get("Cache-Control") != "no-store" || first.Header().Get("X-Request-ID") == "" {
		t.Fatalf("first response status/cache/request-id = %d/%q/%q; body=%s", first.Code, first.Header().Get("Cache-Control"), first.Header().Get("X-Request-ID"), first.Body.String())
	}
	var created struct {
		Invite map[string]any `json:"invite"`
		Room   map[string]any `json:"room"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	roomID, ok := created.Room["id"].(string)
	tokenValue, tokenOK := created.Invite["token"].(string)
	urlValue, urlOK := created.Invite["url"].(string)
	deepLinkValue, deepLinkOK := created.Invite["max_deep_link"].(string)
	if !ok || roomID == "" || !tokenOK || tokenValue == "" || !urlOK || urlValue == "" || !deepLinkOK || deepLinkValue == "" || !strings.Contains(urlValue, tokenValue) || !strings.Contains(deepLinkValue, tokenValue) {
		t.Fatalf("incomplete create DTO: %s", first.Body.String())
	}
	for _, field := range []string{"match", "my_intent", "pool"} {
		if value, exists := created.Room[field]; !exists || value != nil {
			t.Fatalf("room.%s = %#v (present=%v); want explicit null", field, value, exists)
		}
	}
	if _, ok := created.Room["invite"].(map[string]any); !ok {
		t.Fatalf("room invite missing from snapshot: %s", first.Body.String())
	}

	replay := request(body)
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("idempotency replay status/body=%d/%s; want 201 and byte-identical response", replay.Code, replay.Body.String())
	}
	conflict := request(`{"city_id":"` + city.String() + `","name":"different"}`)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"IDEMPOTENCY_CONFLICT"`) {
		t.Fatalf("different idempotency payload status/body=%d/%s", conflict.Code, conflict.Body.String())
	}

	var event struct {
		Origin, Type, RequestID string
		UserID, RoomID          uuid.UUID
	}
	err = db.QueryRow(ctx, `SELECT origin, type, request_id, user_id, room_id FROM behavior_events WHERE room_id=$1`, roomID).Scan(&event.Origin, &event.Type, &event.RequestID, &event.UserID, &event.RoomID)
	if err != nil || event.Origin != "server" || event.Type != "room_created" || event.UserID != user || event.RoomID.String() != roomID || event.RequestID != first.Header().Get("X-Request-ID") {
		t.Fatalf("recorded event=%+v err=%v", event, err)
	}
	var eventCount int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM behavior_events WHERE room_id=$1 AND type='room_created'", roomID).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("room_create behavior rows = %d, %v; want exactly one", eventCount, err)
	}
	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), body) || strings.Contains(logs.String(), tokenValue) {
		t.Fatalf("request secret/body leaked to logs: %s", logs.String())
	}
}

func TestNewHandlerFailsClosedForInviteStartupConfiguration(t *testing.T) {
	for name, mutate := range map[string]func(*config.Config){
		"missing key":        func(c *config.Config) { c.InviteEncryptionKey = nil },
		"missing public URL": func(c *config.Config) { c.InviteURLTemplate = "" },
		"missing max URL":    func(c *config.Config) { c.MAXDeepLinkTemplate = "" },
		"invalid template":   func(c *config.Config) { c.InviteURLTemplate = "http://example.test/{token}" },
		"invalid version":    func(c *config.Config) { c.InviteEncryptionKeyVersion = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := serverTestConfig()
			mutate(&cfg)
			if h, err := newHandler(context.Background(), cfg, nil, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))); err == nil || h != nil {
				t.Fatalf("newHandler() = %v, %v; want nil handler and startup error", h, err)
			}
		})
	}
}

func serverTestConfig() config.Config {
	return config.Config{MAXBotToken: "server-test-bot", MAXInitDataMaxAge: time.Hour, InviteEncryptionKey: bytes.Repeat([]byte{0x54}, 32), InviteEncryptionKeyVersion: 1, InviteURLTemplate: "https://app.test/invite/{token}", MAXDeepLinkTemplate: "https://max.test/bot?startapp={token}", TicketProviderAllowlist: []string{"tickets.example.test"}}
}

func openServerTestDB(t *testing.T) *store.Pool {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(db.Close)
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		t.Fatalf("check migrations: %v", err)
	}
	return db
}

func newServerRoomFixture(t *testing.T, ctx context.Context, db *store.Pool) (uuid.UUID, uuid.UUID, string) {
	t.Helper()
	city, user := uuid.New(), uuid.New()
	if _, err := db.Exec(ctx, "INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'UTC',0,0)", city, "server-room-"+city.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO users(id,max_user_id,display_name,city_id) VALUES($1,$2,$3,$4)", user, atomic.AddInt64(&serverTestMaxUserID, 1), "server room user", city); err != nil {
		t.Fatal(err)
	}
	rawHash := sha256.Sum256([]byte(uuid.NewString()))
	raw := rawHash[:]
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	if _, err := db.Exec(ctx, "INSERT INTO auth_sessions(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')", uuid.New(), user, hash[:]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, statement := range []struct {
			query string
			id    uuid.UUID
		}{
			{"DELETE FROM behavior_events WHERE user_id=$1", user},
			{"DELETE FROM idempotency_records WHERE user_id=$1", user},
			{"DELETE FROM auth_sessions WHERE user_id=$1", user},
			{"DELETE FROM rooms WHERE creator_user_id=$1", user},
			{"DELETE FROM users WHERE id=$1", user},
			{"DELETE FROM cities WHERE id=$1", city},
		} {
			if _, err := db.Exec(cleanupCtx, statement.query, statement.id); err != nil {
				t.Errorf("cleanup %q: %v", statement.query, err)
			}
		}
	})
	return city, user, token
}
