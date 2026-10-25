package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/discovery"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
)

// This opt-in harness runs actual browser clients against the room HTTP handlers,
// real recommendation builder and isolated PostgreSQL. Only MAX authentication
// is replaced by test principals. No production server or external catalog is used.
func TestRoomRoundsBrowser(t *testing.T) {
	if os.Getenv("RUN_ROOMS_BROWSER_E2E") != "1" {
		t.Skip("set RUN_ROOMS_BROWSER_E2E=1 to run the browser harness")
	}
	config := os.Getenv("ROOMS_BROWSER_CONFIG")
	if config == "" {
		config, _ = filepath.Abs("../../../frontend/playwright.rooms-live.config.ts")
	}
	for _, project := range []string{"mobile-chrome", "desktop-chrome"} {
		t.Run(project, func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			f := newRoomFixture(t, db)
			f.addTwoMembers(t)
			var event uuid.UUID
			// Register before any setup mutation so failed setup is also cleaned.
			t.Cleanup(func() {
				_, _ = db.Exec(ctx, "DELETE FROM behavior_events WHERE room_id=$1", f.room)
				_, _ = db.Exec(ctx, "DELETE FROM room_matches WHERE room_id=$1", f.room)
				_, _ = db.Exec(ctx, "DELETE FROM rooms WHERE id=$1", f.room)
				var venue uuid.UUID
				_ = db.QueryRow(ctx, "SELECT venue_id FROM events WHERE id=$1", event).Scan(&venue)
				_, _ = db.Exec(ctx, "DELETE FROM events WHERE id=$1", event)
				_, _ = db.Exec(ctx, "DELETE FROM venues WHERE id=$1", venue)
			})
			moscow := uuid.MustParse("a0f625ee-2154-5a45-8afe-37adf955ec24")
			if _, err := db.Exec(ctx, "INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,'Москва','Europe/Moscow',55.75,37.62) ON CONFLICT(id) DO NOTHING", moscow); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "UPDATE rooms SET city_id=$2,name='Встреча' WHERE id=$1", f.room, moscow); err != nil {
				t.Fatal(err)
			}
			event = f.newEvent(t)
			zone, _ := time.LoadLocation("Europe/Moscow")
			now := time.Now().In(zone)
			firstDay := now.AddDate(0, 0, 1).Format("2006-01-02")
			secondDay := now.AddDate(0, 0, 2).Format("2006-01-02")
			start, _ := time.ParseInLocation("2006-01-02 15:04", secondDay+" 19:00", zone)
			if _, err := db.Exec(ctx, "UPDATE venues SET city_id=$2 WHERE id=(SELECT venue_id FROM events WHERE id=$1)", event, moscow); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "UPDATE events SET title='Концерт для двоих',starts_at=$2::timestamptz,ends_at=$2::timestamptz+interval '2 hours',timezone='Europe/Moscow',price_from_minor=100,ticket_url='https://tickets.example/rounds',ticket_available=true WHERE id=$1", event, start); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "INSERT INTO event_categories(event_id,category_slug,is_primary) VALUES($1,'concerts',true)", event); err != nil {
				t.Fatal(err)
			}
			for _, user := range []uuid.UUID{f.creator, f.member} {
				if _, err := db.Exec(ctx, "INSERT INTO room_member_round_state(room_id,user_id,round_no) VALUES($1,$2,1)", f.room, user); err != nil {
					t.Fatal(err)
				}
			}
			builder, err := recommendations.NewPoolBuilder(catalog.NewRepository(db), []byte("0123456789abcdef0123456789abcdef"))
			if err != nil {
				t.Fatal(err)
			}
			svc := newCreateServiceWithBuilder(t, db, behavior.Recorder{}, builder)
			cursor, _ := rooms.NewRoomEventsCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
			if err := svc.EnableRoomEvents(catalog.NewRepository(db), cursor); err != nil {
				t.Fatal(err)
			}
			for i, user := range []uuid.UUID{f.creator, f.member} {
				day := firstDay
				if i == 1 {
					day = secondDay
				}
				d, _ := time.Parse("2006-01-02", day)
				req := validIntentRequest()
				req.Dates = []openapi_types.Date{{Time: d}}
				req.DayTypes = []api.DayType{}
				req.TimeSlots = []api.TimeSlot{api.Evening}
				req.ExclusionSlugs = []api.RoomIntentRequestExclusionSlugs{}
				req.Location = nullable.NewNullNullable[api.GeoPoint]()
				req.RadiusM = nullable.NewNullNullable[int]()
				req.FreeText = nullable.NewNullableWithValue("Личное пожелание " + fmt.Sprint(i))
				if _, _, err := replaceIntentFromSnapshot(t, svc, f, user, req); err != nil {
					t.Fatal(err)
				}
			}
			initial, err := svc.Get(ctx, contracts.Principal{UserID: f.creator}, f.room)
			initialPool, poolErr := initial.Pool.Get()
			if err != nil || poolErr != nil || initial.State != api.RoomStateExhausted || initialPool.Total != 0 {
				t.Fatalf("incompatible dates must produce empty pool: %+v, %v", initial, err)
			}
			r := chi.NewRouter()
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					user, err := uuid.Parse(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
					if err != nil || user != f.creator && user != f.member {
						http.Error(w, "test principal required", 401)
						return
					}
					next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: user})))
				})
			})
			svc.RegisterRoutes(r)
			r.Get("/api/v1/events/{eventId}", discovery.NewDetailHandler(discovery.NewRepository(db)).GetEvent)
			server := httptest.NewServer(r)
			defer server.Close()
			frontend, _ := filepath.Abs("../../../frontend")
			command := exec.CommandContext(ctx, "node", filepath.Join(frontend, "node_modules/@playwright/test/cli.js"), "test", "--config", config, "--project", project)
			command.Dir = frontend
			command.Env = append(os.Environ(), "ROOMS_LIVE_API="+server.URL, "ROOMS_LIVE_ROOM="+f.room.String(), "ROOMS_LIVE_A="+f.creator.String(), "ROOMS_LIVE_B="+f.member.String(), "ROOMS_LIVE_DATE="+secondDay)
			out, err := command.CombinedOutput()
			t.Log(string(out))
			if err != nil {
				t.Fatalf("Playwright: %v", err)
			}
			var pools, votes int
			_ = db.QueryRow(ctx, "SELECT count(*) FROM room_pools WHERE room_id=$1", f.room).Scan(&pools)
			_ = db.QueryRow(ctx, "SELECT count(*) FROM room_votes WHERE room_id=$1", f.room).Scan(&votes)
			if pools != 3 || votes != 2 {
				t.Fatalf("persistence pools=%d votes=%d, want3/2", pools, votes)
			}
		})
	}
}
