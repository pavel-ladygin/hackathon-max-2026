package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
)

// protocolFenceLab is a TEST-ONLY transition prototype. It is intentionally not
// registered by cmd/server. Production activation, legacy-draft recovery, and
// the rollback image floor need a separately approved release decision.
// Crucially, it never supplies an expected room version or confirms an intent.
func protocolFenceLab(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Room-Protocol", "2")
		if r.URL.Path == "/api/v1/rooms/protocol" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"room_protocol":2,"intent_source_required":true,"next_round_requires_both":true}`)
			return
		}
		roomRoute := r.URL.Path == "/api/v1/rooms" || strings.HasPrefix(r.URL.Path, "/api/v1/rooms/") || strings.HasPrefix(r.URL.Path, "/api/v1/room-invites/")
		if roomRoute && r.Header.Get("X-Room-Protocol") != "2" {
			w.Header().Set("Upgrade", "max-together-room/2")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUpgradeRequired)
			_, _ = io.WriteString(w, `{"error":{"code":"ROOM_PROTOCOL_UPGRADE_REQUIRED","message":"Нужна новая версия приложения. Подтверждённые условия сохранены. Не перезагружайте экран, пока не сохраните свой незавершённый ввод."}}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// This reproduces the pre-PR84 decoder's unknown-field boundary, not its
// business logic. Actual old SQL compatibility is tested against PostgreSQL
// below, so this transport stub is not presented as an old production server.
func legacyIntentDecoderLab(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/rooms/protocol" {
		http.NotFound(w, r)
		return
	}
	var legacy struct {
		Dates          json.RawMessage `json:"dates"`
		DayTypes       json.RawMessage `json:"day_types"`
		TimeSlots      json.RawMessage `json:"time_slots"`
		CategorySlugs  json.RawMessage `json:"category_slugs"`
		BudgetMaxMinor json.RawMessage `json:"budget_max_minor"`
		RadiusM        json.RawMessage `json:"radius_m"`
		ExclusionSlugs json.RawMessage `json:"exclusion_slugs"`
		Location       json.RawMessage `json:"location"`
		FreeText       json.RawMessage `json:"free_text"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&legacy); err != nil {
		http.Error(w, "legacy decoder rejects unknown fields", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func labRequest(t *testing.T, handler http.Handler, method, path string, body []byte, protocol bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if protocol {
		r.Header.Set("Idempotency-Key", "protocol-lab-key")
		r.Header.Set("X-Room-Protocol", "2")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestRoomProtocolTransitionHealthSmokeAndAllRoomRoutes(t *testing.T) {
	called := 0
	gate := protocolFenceLab(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		_, _ = io.WriteString(w, `{"status":"ready"}`)
	}))
	health := labRequest(t, gate, http.MethodGet, "/api/v1/health/ready", nil, false)
	if health.Code != 200 || called != 1 || !strings.Contains(health.Body.String(), "ready") {
		t.Fatalf("health smoke=%d calls=%d", health.Code, called)
	}
	for _, path := range []string{"/api/v1/rooms", "/api/v1/room-invites/legacy/join", "/api/v1/rooms/existing/events"} {
		w := labRequest(t, gate, http.MethodPost, path, []byte(`{}`), false)
		if w.Code != 426 || called != 1 {
			t.Fatalf("unfenced legacy route %s status=%d calls=%d", path, w.Code, called)
		}
	}
}

func TestRoomProtocolTransitionTransportMatrix(t *testing.T) {
	f, svc := setupPreparation(t, &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "protocol-lab", InputFingerprint: "protocol-lab"}})
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: f.creator})))
		})
	})
	svc.RegisterRoutes(router)
	request := preparationReq(1, roomVersion(t, f))
	modern, _ := json.Marshal(request)
	var legacyFields map[string]json.RawMessage
	_ = json.Unmarshal(modern, &legacyFields)
	delete(legacyFields, "expected_round_no")
	delete(legacyFields, "expected_room_version")
	legacy, _ := json.Marshal(legacyFields)
	path := "/api/v1/rooms/" + f.room.String() + "/intent/me"
	old := http.HandlerFunc(legacyIntentDecoderLab)
	for _, tc := range []struct {
		name string
		h    http.Handler
		body []byte
		v2   bool
		want int
	}{
		{"old-client_old-decoder", old, legacy, false, 200},
		{"new-client_old-decoder_rejected", old, modern, true, 400},
		{"old-client_strict-new-server_rejected", router, legacy, false, 400},
		{"old-client_prototype-fence_upgrade", protocolFenceLab(router), legacy, false, 426},
		{"unknown-client_prototype-fence_upgrade", protocolFenceLab(router), modern, false, 426},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := preparationFactsFor(t, f)
			w := labRequest(t, tc.h, http.MethodPut, path, tc.body, tc.v2)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
			if after := preparationFactsFor(t, f); after != before {
				t.Fatalf("transport changed room: before=%+v after=%+v", before, after)
			}
		})
	}
	t.Run("new-client_negotiates_and_submits_explicit_source", func(t *testing.T) {
		gate := protocolFenceLab(router)
		capability := labRequest(t, gate, http.MethodGet, "/api/v1/rooms/protocol", nil, false)
		if capability.Code != 200 || !strings.Contains(capability.Body.String(), `"room_protocol":2`) {
			t.Fatalf("capability=%d %s", capability.Code, capability.Body.String())
		}
		w := labRequest(t, gate, http.MethodPut, path, modern, true)
		if w.Code != 200 {
			t.Fatalf("guarded submit=%d %s", w.Code, w.Body.String())
		}
		facts := preparationFactsFor(t, f)
		if facts.round != 1 || facts.pools != 0 || facts.votes != 0 {
			t.Fatalf("first submit launched a round: %+v", facts)
		}
	})
	t.Run("new-client_old-server_no_capability_no_PUT", func(t *testing.T) {
		puts := 0
		oldCounted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				puts++
			}
			legacyIntentDecoderLab(w, r)
		})
		capability := labRequest(t, oldCounted, http.MethodGet, "/api/v1/rooms/protocol", nil, false)
		// The prototype client fails closed. There is no legacy write fallback.
		if capability.Code == 200 {
			t.Fatal("old server unexpectedly advertises v2")
		}
		if puts != 0 {
			t.Fatalf("negotiation silently attempted %d legacy writes", puts)
		}
	})
}

func TestRoomProtocolTransitionOldTabCannotReadOrConfirmPendingRound(t *testing.T) {
	f, svc := setupPreparation(t, &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "protocol-pending", InputFingerprint: "protocol-pending"}})
	version := exhaustZeroRound(t, svc, f, 1, roomVersion(t, f), "protocol-pending")
	if _, _, err := svc.ReplaceIntent(context.Background(), contracts.Principal{UserID: f.creator}, f.room, preparationReq(1, version), "protocol-prepare"); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: f.member})))
		})
	})
	svc.RegisterRoutes(router)
	gate := protocolFenceLab(router)
	before := preparationFactsFor(t, f)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		path := "/api/v1/rooms/" + f.room.String()
		if method == http.MethodPut {
			path += "/intent/me"
		}
		w := labRequest(t, gate, method, path, []byte(`{}`), false)
		if w.Code != 426 || strings.Contains(w.Body.String(), "next_round") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("legacy request=%d %s headers=%v", w.Code, w.Body.String(), w.Header())
		}
	}
	if after := preparationFactsFor(t, f); after != before {
		t.Fatalf("old tab confirmed or changed room: %+v -> %+v", before, after)
	}
	modernRead := labRequest(t, gate, http.MethodGet, "/api/v1/rooms/"+f.room.String(), nil, true)
	if modernRead.Code != 200 || !strings.Contains(modernRead.Body.String(), `"next_round"`) {
		t.Fatalf("v2 pending read=%d %s", modernRead.Code, modernRead.Body.String())
	}
}

func TestRoomProtocolTransitionRollbackWithPendingRows(t *testing.T) {
	f, svc := setupPreparation(t, &poolBuilderFake{result: contracts.BuildResult{RankerVersion: "rollback-lab", InputFingerprint: "rollback-lab"}})
	ctx := context.Background()
	version := exhaustZeroRound(t, svc, f, 1, roomVersion(t, f), "rollback")
	first := preparationReq(1, version)
	first.BudgetMaxMinor += 50000
	if _, _, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: f.creator}, f.room, first, "rollback-prepare"); err != nil {
		t.Fatal(err)
	}
	before := preparationFactsFor(t, f)
	if before.round != 1 || before.nextReady != 1 {
		t.Fatalf("not a pending fixture: %+v", before)
	}
	// Exact pre-PR84 operation order: open N+1, then copy intents with no
	// ON CONFLICT clause. Real PostgreSQL must reject the existing N+1 rows.
	db := preparationPool(t, f)
	oldErr := db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE rooms SET round_no=round_no+1,state='collecting_intents' WHERE id=$1`, f.room); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO room_intents(room_id,user_id,round_no,date_options,day_types,time_slots,category_slugs,budget_max_minor,location_lat,location_lng,radius_m,exclusion_slugs,free_text)
SELECT i.room_id,i.user_id,2,i.date_options,i.day_types,i.time_slots,i.category_slugs,i.budget_max_minor,i.location_lat,i.location_lng,i.radius_m,i.exclusion_slugs,i.free_text
FROM room_intents i JOIN room_members m ON m.room_id=i.room_id AND m.user_id=i.user_id AND m.is_active=true WHERE i.room_id=$1 AND i.round_no=1`, f.room)
		return err
	})
	pgErr, ok := oldErr.(*pgconn.PgError)
	if !ok || pgErr.Code != "23505" {
		t.Fatalf("old SQL should fail duplicate row: %v", oldErr)
	}
	if after := preparationFactsFor(t, f); after != before {
		t.Fatalf("failed rollback simulation lost pending data: %+v -> %+v", before, after)
	}
	// The published PR84 floor (6258042) and current integration share this
	// guarded pending-row implementation. Fresh explicit peer confirmation is
	// safe; deleting pending data or replaying a legacy PUT is not a rollback.
	if _, transitioned, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: f.member}, f.room, preparationReq(1, roomVersion(t, f)), "rollback-peer-confirm"); err != nil || !transitioned {
		t.Fatalf("compatible confirmation transitioned=%v err=%v", transitioned, err)
	}
	after := preparationFactsFor(t, f)
	if after.round != 2 || after.pools != 2 || after.votes != 0 {
		t.Fatalf("compatible code did not advance exactly once: %+v", after)
	}
	var budget int
	if err := db.QueryRow(ctx, `SELECT budget_max_minor FROM room_intents WHERE room_id=$1 AND user_id=$2 AND round_no=2`, f.room, f.creator).Scan(&budget); err != nil || budget != first.BudgetMaxMinor {
		t.Fatalf("prepared preference lost: budget=%d err=%v", budget, err)
	}
}
