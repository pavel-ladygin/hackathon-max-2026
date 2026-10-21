package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/kudago"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
)

// Two independent worker loops and pools exercise the same scheduling, actual
// PostgreSQL advisory locks, importer and persistence as separate processes.
func TestMulticityWorkersRetryAfterProviderLockContention(t *testing.T) {
	dsn := os.Getenv("MULTICITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated multicity DB required")
	}
	if _, err := testdbguard.Parse(dsn, "multicity_fixture"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstDB, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer firstDB.Close()
	secondDB, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	cities := []uuid.UUID{uuid.New(), uuid.New()}
	for _, city := range cities {
		if _, err := firstDB.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,'Worker fixture','Europe/Moscow',55,49)`, city); err != nil {
			t.Fatal(err)
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	ids := []uint32{uuid.New().ID(), uuid.New().ID()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		location := r.URL.Query().Get("location")
		index := 0
		if location == "msk" {
			enterOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		} else if location == "kzn" {
			index = 1
		} else {
			http.Error(w, "bad fixture city", 400)
			return
		}
		start := time.Now().UTC().Add(24 * time.Hour)
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "next": nil, "results": []any{map[string]any{
			"id": ids[index], "title": "Synthetic worker fixture", "location": map[string]any{"slug": location},
			"dates":      []any{map[string]any{"start": start.Unix(), "end": start.Add(time.Hour).Unix()}},
			"categories": []string{"concert"}, "is_free": true, "site_url": "https://kudago.com/event/worker-fixture/",
			"place": map[string]any{"id": ids[index], "title": "Fixture", "address": "Fixture", "coords": map[string]any{"lat": 55, "lon": 49}},
		}}})
	}))
	defer func() {
		releaseOnce.Do(func() { close(release) })
		cancel()
		server.Close()
	}()
	makeProvider := func(db *store.Pool, city uuid.UUID, location string) syncProvider {
		importer, err := kudago.NewClient(kudago.Options{BaseURL: server.URL, Timeout: 5 * time.Second, PageSize: 100, CityID: city, Timezone: "Europe/Moscow", Location: location})
		if err != nil {
			t.Fatal(err)
		}
		return syncProvider{name: "kudago", importer: importer, acquireLock: func(ctx context.Context) (func() error, bool, error) {
			lock, ok, err := providers.TryAcquireProviderSyncLock(ctx, db, "kudago")
			if lock == nil {
				return nil, ok, err
			}
			return lock.Release, ok, err
		}}
	}
	a, b := makeProvider(firstDB, cities[0], "msk"), makeProvider(secondDB, cities[1], "kzn")
	aDone, bDone := make(chan error, 1), make(chan error, 1)
	go func() {
		var result error
		runSyncLoop(ctx, 10*time.Millisecond, true, func() bool {
			result = syncOne(ctx, cities[0], providers.NewRepository(firstDB), a, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
			return false
		})
		aDone <- result
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first worker never acquired provider lock")
	}
	var bLogs bytes.Buffer
	go func() {
		busy := 0
		var result error
		runSyncLoop(ctx, 10*time.Millisecond, true, func() bool {
			result = syncOne(ctx, cities[1], providers.NewRepository(secondDB), b, slog.New(slog.NewJSONHandler(&bLogs, nil)))
			if errors.Is(result, errProviderSyncBusy) {
				busy++
				if busy >= 2 {
					releaseOnce.Do(func() { close(release) })
				}
				return true
			}
			return false
		})
		bDone <- result
	}()
	select {
	case err := <-aDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("first worker timed out")
	}
	select {
	case err := <-bDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("second worker did not retry successfully")
	}
	if !bytes.Contains(bLogs.Bytes(), []byte(`"final_status":"skipped"`)) || !bytes.Contains(bLogs.Bytes(), []byte(`"final_status":"succeeded"`)) {
		t.Fatal("busy skip not distinct from later successful import")
	}
	for _, city := range cities {
		var runs, events int
		if err := firstDB.QueryRow(ctx, `SELECT count(*) FROM provider_sync_runs WHERE provider='kudago' AND city_id=$1 AND state='succeeded'`, city).Scan(&runs); err != nil {
			t.Fatal(err)
		}
		if err := firstDB.QueryRow(ctx, `SELECT count(*) FROM events e JOIN venues v ON v.id=e.venue_id WHERE e.source='kudago' AND e.provider_active AND v.city_id=$1`, city).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if runs != 1 || events != 1 {
			t.Fatalf("city %s: succeeded runs=%d active events=%d", city, runs, events)
		}
	}
}
