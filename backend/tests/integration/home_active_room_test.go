package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/discovery"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
)

type emptyHomeSearch struct{}

func (emptyHomeSearch) Search(context.Context, discovery.SearchFilter) (discovery.Page, error) {
	return discovery.Page{}, nil
}

type emptyHomePreferences struct{}

func (emptyHomePreferences) Get(context.Context, uuid.UUID) (preferences.Value, bool, error) {
	return preferences.Value{}, false, nil
}

func TestHomeFeedReturnsUsersActiveRoom(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()

	if _, err := db.Exec(ctx, "INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'creator')", f.room, f.creator); err != nil {
		t.Fatal(err)
	}

	repository := discovery.NewRepository(db)
	home := discovery.NewHomeService(emptyHomeSearch{}, repository, emptyHomePreferences{}, repository)
	handler := discovery.NewHomeHandler(home)
	router := chi.NewRouter()
	router.Get("/api/v1/feed/home", handler.GetHome)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/feed/home", nil)
	request = request.WithContext(contracts.WithPrincipal(request.Context(), contracts.Principal{UserID: f.creator}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var feed api.HomeFeedResponse
	if err := json.Unmarshal(response.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode home response: %v", err)
	}
	activeRoom, err := feed.ActiveRoom.Get()
	if err != nil {
		t.Fatalf("active_room is null for live membership: room_id=%s state=collecting_intents", f.room)
	}
	if activeRoom.Id != f.room || activeRoom.Name != "test" || activeRoom.CityId != f.city || activeRoom.State != api.RoomStateCollectingIntents {
		t.Fatalf("active_room=%+v; want room=%s city=%s state=collecting_intents", activeRoom, f.room, f.city)
	}
}

func TestDiscoveryRepositoryReturnsOnlyLiveActiveRoom(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	repository := discovery.NewRepository(db)

	for _, state := range []string{"collecting_intents", "ranking", "voting"} {
		t.Run(state, func(t *testing.T) {
			f := newRoomFixture(t, db)
			if _, err := db.Exec(ctx, "UPDATE rooms SET state=$2 WHERE id=$1", f.room, state); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'creator')", f.room, f.creator); err != nil {
				t.Fatal(err)
			}
			active, found, err := repository.GetActiveRoom(ctx, f.creator)
			if err != nil || !found || active.ID != f.room || active.Name != "test" || active.CityID != f.city || active.State != state {
				t.Fatalf("active=%+v found=%t err=%v", active, found, err)
			}
			if active, found, err := repository.GetActiveRoom(ctx, f.member); err != nil || found {
				t.Fatalf("room leaked to non-member: active=%+v found=%t err=%v", active, found, err)
			}
		})
	}

	for _, state := range []string{"matched", "exhausted"} {
		t.Run(state, func(t *testing.T) {
			f := newRoomFixture(t, db)
			if _, err := db.Exec(ctx, "UPDATE rooms SET state=$2 WHERE id=$1", f.room, state); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'creator')", f.room, f.creator); err != nil {
				t.Fatal(err)
			}
			if active, found, err := repository.GetActiveRoom(ctx, f.creator); err != nil || found {
				t.Fatalf("terminal room returned: active=%+v found=%t err=%v", active, found, err)
			}
		})
	}

	t.Run("expired", func(t *testing.T) {
		f := newRoomFixture(t, db)
		if _, err := db.Exec(ctx, "UPDATE rooms SET expires_at=now()-interval '1 second' WHERE id=$1", f.room); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'creator')", f.room, f.creator); err != nil {
			t.Fatal(err)
		}
		if active, found, err := repository.GetActiveRoom(ctx, f.creator); err != nil || found {
			t.Fatalf("expired room returned: active=%+v found=%t err=%v", active, found, err)
		}
	})

	t.Run("inactive membership", func(t *testing.T) {
		f := newRoomFixture(t, db)
		if _, err := db.Exec(ctx, "INSERT INTO room_members(room_id,user_id,role,is_active) VALUES($1,$2,'creator',false)", f.room, f.creator); err != nil {
			t.Fatal(err)
		}
		if active, found, err := repository.GetActiveRoom(ctx, f.creator); err != nil || found {
			t.Fatalf("inactive membership returned: active=%+v found=%t err=%v", active, found, err)
		}
	})
}
