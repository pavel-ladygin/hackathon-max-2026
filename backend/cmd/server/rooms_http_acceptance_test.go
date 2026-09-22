package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestHTTPRoomTwoClientMatchAcceptance exercises the production HTTP router with
// MAX-authenticated users, the PostgreSQL-backed catalog and the real pool builder.
func TestHTTPRoomTwoClientMatchAcceptance(t *testing.T) {
	db := openServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cityID, venueID, eventID := uuid.New(), uuid.New(), uuid.New()
	date := time.Now().UTC().AddDate(0, 0, 2)
	startsAt := time.Date(date.Year(), date.Month(), date.Day(), 18, 0, 0, 0, time.UTC)
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,$2,'UTC',55.75,37.61)`, cityID, "http-acceptance-"+cityID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO venues(id,city_id,name,address,latitude,longitude,venue_type) VALUES($1,$2,'HTTP acceptance venue','test address',55.75,37.61,'concert_hall')`, venueID, cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO events(id,source,external_id,is_demo,title,description,venue_id,starts_at,timezone,price_from_minor,currency,ticket_url,ticket_available,status) VALUES($1,'http-acceptance',$2,false,'HTTP acceptance event','test event',$3,$4,'UTC',1000,'RUB','https://tickets.example.test/http-acceptance',true,'published')`, eventID, eventID.String(), venueID, startsAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO event_categories(event_id,category_slug,is_primary) VALUES($1,'concerts',true)`, eventID); err != nil {
		t.Fatal(err)
	}

	var creatorID, participantID uuid.UUID
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, stmt := range []struct {
			query string
			args  []any
		}{
			{"DELETE FROM behavior_events WHERE user_id = ANY($1)", []any{[]uuid.UUID{creatorID, participantID}}},
			{"DELETE FROM idempotency_records WHERE user_id = ANY($1)", []any{[]uuid.UUID{creatorID, participantID}}},
			{"DELETE FROM room_matches WHERE room_id IN (SELECT id FROM rooms WHERE creator_user_id = $1)", []any{creatorID}},
			{"DELETE FROM rooms WHERE creator_user_id = $1", []any{creatorID}},
			{"DELETE FROM users WHERE id = ANY($1)", []any{[]uuid.UUID{creatorID, participantID}}},
			{"DELETE FROM events WHERE id = $1", []any{eventID}},
			{"DELETE FROM venues WHERE id = $1", []any{venueID}},
			{"DELETE FROM cities WHERE id = $1", []any{cityID}},
		} {
			if _, err := db.Exec(cleanupCtx, stmt.query, stmt.args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt.query, err)
			}
		}
	})

	h, err := newHandler(context.Background(), serverTestConfig(), db, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, target, token string, body any, idempotencyKey string) *httptest.ResponseRecorder {
		t.Helper()
		var reader *strings.Reader
		if body == nil {
			reader = strings.NewReader("")
		} else {
			encoded, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			reader = strings.NewReader(string(encoded))
		}
		req := httptest.NewRequest(method, target, reader)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res
	}
	bootstrap := func(maxID int64, name, startParam string) bootstrapHTTPResponse {
		t.Helper()
		initData := signedHTTPAcceptanceMAXInitData(t, maxID, name, startParam)
		payload := map[string]string{"init_data": initData}
		if startParam != "" {
			payload["start_param"] = startParam
		}
		res := request(http.MethodPost, "/api/v1/auth/max/bootstrap", "", payload, "")
		if res.Code != http.StatusOK {
			t.Fatalf("bootstrap %s status=%d body=%s", name, res.Code, res.Body.String())
		}
		var decoded bootstrapHTTPResponse
		if err := json.Unmarshal(res.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.AccessToken == "" || decoded.User.ID == uuid.Nil {
			t.Fatalf("bootstrap %s returned incomplete response: %s", name, res.Body.String())
		}
		return decoded
	}

	maxBase := time.Now().UnixNano()
	creator := bootstrap(maxBase, "Creator", "")
	creatorID = creator.User.ID
	create := request(http.MethodPost, "/api/v1/rooms", creator.AccessToken, map[string]string{"city_id": cityID.String(), "name": "HTTP two-client room"}, "http-acceptance-create")
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var created createRoomHTTPResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Room.ID == uuid.Nil || created.Invite.Token == "" || created.Room.State != "collecting_intents" {
		t.Fatalf("create response is incomplete: %s", create.Body.String())
	}

	participant := bootstrap(maxBase+1, "Participant", created.Invite.Token)
	participantID = participant.User.ID
	if participant.InviteContext == nil || participant.InviteContext.Token != created.Invite.Token || participant.InviteContext.RoomName != "HTTP two-client room" || participant.InviteContext.Status != "joinable" {
		t.Fatalf("invite bootstrap context=%+v; want joinable context for the created room", participant.InviteContext)
	}
	join := request(http.MethodPost, "/api/v1/room-invites/"+url.PathEscape(created.Invite.Token)+"/join", participant.AccessToken, nil, "http-acceptance-join")
	if join.Code != http.StatusOK {
		t.Fatalf("join status=%d body=%s", join.Code, join.Body.String())
	}
	var joined roomHTTPResponse
	if err := json.Unmarshal(join.Body.Bytes(), &joined); err != nil {
		t.Fatal(err)
	}
	if joined.ID != created.Room.ID || len(joined.Participants) != 2 {
		t.Fatalf("join snapshot=%+v; want two users in %s", joined, created.Room.ID)
	}

	intent := map[string]any{
		"dates":            []string{startsAt.Format(time.DateOnly)},
		"day_types":        []string{},
		"time_slots":       []string{},
		"category_slugs":   []string{"concerts"},
		"budget_max_minor": 1000,
		"exclusion_slugs":  []string{},
	}
	for _, client := range []struct {
		name, token string
	}{
		{"creator", creator.AccessToken},
		{"participant", participant.AccessToken},
	} {
		res := request(http.MethodPut, "/api/v1/rooms/"+created.Room.ID.String()+"/intent/me", client.token, intent, "")
		if res.Code != http.StatusOK {
			t.Fatalf("%s intent status=%d body=%s", client.name, res.Code, res.Body.String())
		}
	}

	pool := request(http.MethodGet, "/api/v1/rooms/"+created.Room.ID.String()+"/events", creator.AccessToken, nil, "")
	if pool.Code != http.StatusOK {
		t.Fatalf("pool status=%d body=%s", pool.Code, pool.Body.String())
	}
	var events struct {
		PoolVersion int `json:"pool_version"`
		Items       []struct {
			Event struct {
				ID uuid.UUID `json:"id"`
			} `json:"event"`
		} `json:"items"`
	}
	if err := json.Unmarshal(pool.Body.Bytes(), &events); err != nil {
		t.Fatal(err)
	}
	if events.PoolVersion != 1 || len(events.Items) != 1 || events.Items[0].Event.ID != eventID {
		t.Fatalf("pool=%s; want exactly seeded event %s in version 1", pool.Body.String(), eventID)
	}

	votePayload := map[string]any{"pool_version": events.PoolVersion, "vote": "like"}
	firstVote := request(http.MethodPut, "/api/v1/rooms/"+created.Room.ID.String()+"/events/"+eventID.String()+"/vote", creator.AccessToken, votePayload, "")
	if firstVote.Code != http.StatusOK {
		t.Fatalf("first vote status=%d body=%s", firstVote.Code, firstVote.Body.String())
	}
	secondVote := request(http.MethodPut, "/api/v1/rooms/"+created.Room.ID.String()+"/events/"+eventID.String()+"/vote", participant.AccessToken, votePayload, "")
	if secondVote.Code != http.StatusOK {
		t.Fatalf("second vote status=%d body=%s", secondVote.Code, secondVote.Body.String())
	}
	var matched struct {
		Match *struct {
			ID    uuid.UUID `json:"id"`
			Event struct {
				ID uuid.UUID `json:"id"`
			} `json:"event"`
		} `json:"match"`
	}
	if err := json.Unmarshal(secondVote.Body.Bytes(), &matched); err != nil {
		t.Fatal(err)
	}
	if matched.Match == nil || matched.Match.ID == uuid.Nil || matched.Match.Event.ID != eventID {
		t.Fatalf("second vote match=%s; want mutual match for %s", secondVote.Body.String(), eventID)
	}

	for _, client := range []struct {
		name, token string
	}{
		{"creator", creator.AccessToken},
		{"participant", participant.AccessToken},
	} {
		res := request(http.MethodGet, "/api/v1/rooms/"+created.Room.ID.String(), client.token, nil, "")
		if res.Code != http.StatusOK {
			t.Fatalf("%s reload status=%d body=%s", client.name, res.Code, res.Body.String())
		}
		var snapshot roomHTTPResponse
		if err := json.Unmarshal(res.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.State != "matched" || snapshot.Match == nil || snapshot.Match.ID != matched.Match.ID || snapshot.Match.EventID != eventID {
			t.Fatalf("%s reload snapshot=%s; want persisted match %s for event %s", client.name, res.Body.String(), matched.Match.ID, eventID)
		}
	}
}

type bootstrapHTTPResponse struct {
	AccessToken string `json:"access_token"`
	User        struct {
		ID uuid.UUID `json:"id"`
	} `json:"user"`
	InviteContext *struct {
		Token    string `json:"token"`
		RoomName string `json:"room_name"`
		Status   string `json:"status"`
	} `json:"invite_context"`
}

type createRoomHTTPResponse struct {
	Invite struct {
		Token string `json:"token"`
	} `json:"invite"`
	Room roomHTTPResponse `json:"room"`
}

type roomHTTPResponse struct {
	ID           uuid.UUID `json:"id"`
	State        string    `json:"state"`
	Participants []any     `json:"participants"`
	Match        *struct {
		ID      uuid.UUID `json:"id"`
		EventID uuid.UUID `json:"event_id"`
	} `json:"match"`
}

func signedHTTPAcceptanceMAXInitData(t *testing.T, maxID int64, name, startParam string) string {
	t.Helper()
	user, err := json.Marshal(map[string]any{"id": maxID, "first_name": name, "last_name": "Acceptance", "language_code": "ru"})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"auth_date": strconv.FormatInt(time.Now().Unix(), 10), "user": string(user)}
	if startParam != "" {
		values["start_param"] = startParam
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	// The signed MAX payload requires lexical key order.
	slices.Sort(keys)
	check := make([]string, 0, len(keys))
	for _, key := range keys {
		check = append(check, key+"="+values[key])
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secret.Write([]byte(serverTestConfig().MAXBotToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	_, _ = mac.Write([]byte(strings.Join(check, "\n")))
	pairs := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		pairs = append(pairs, key+"="+url.PathEscape(values[key]))
	}
	pairs = append(pairs, "hash="+hex.EncodeToString(mac.Sum(nil)))
	return strings.Join(pairs, "&")
}
