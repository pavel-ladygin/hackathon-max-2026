package timepad

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func TestImportHundredEventsWithOneNonstandardTicketTypes(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		tickets  string
		fallback bool
	}{
		{"opaque unknown price", `{"unknown":{"price":0,"is_active":true}}`, false},
		{"opaque registration price", `{"unknown":{"price":0,"is_active":true}}`, true},
		{"incomplete unknown price", `[{"price":0,"is_active":true},{"price":"10","is_active":true}]`, false},
		{"incomplete registration price", `[{"price":0,"is_active":true},{"price":"10","is_active":true}]`, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fallback := scenario.fallback
			var events []string
			for id := 1; id <= 100; id++ {
				tickets := `[{"price":125.50,"is_active":true},{"price":250,"is_active":true}]`
				registration := `{"is_registration_open":true}`
				// Reproduce values[20] with unsupported or incomplete ticket data.
				if id == 21 {
					tickets = scenario.tickets
					if fallback {
						registration = `{"price_min":400,"price_max":700,"is_registration_open":true}`
					}
				}
				events = append(events, fmt.Sprintf(`{"id":%d,"name":"Event %d","starts_at":"2026-10-01T07:00:00Z","location":{"city":"Санкт-Петербург","address":"Street"},"description_short":"Kept","poster_image":{"default_url":"https://cdn.example.test/poster.jpg"},"ticket_types":%s,"registration_data":%s}`, id, id, tickets, registration))
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/api/events.json" || r.URL.Query().Get("skip") != "0" || r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("cities") != "Санкт-Петербург" {
					t.Errorf("unexpected request: %s", r.URL)
				}
				fmt.Fprintf(w, `{"total":100,"values":[%s]}`, strings.Join(events, ","))
			}))
			defer server.Close()
			client := testClient(t, server)
			client.cityName = "Санкт-Петербург"
			client.pageSize = 100
			client.upsertOnly = true
			client.now = func() time.Time { return time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC) }
			store := &memoryEventStore{seen: make(map[string]struct{})}
			stats, err := client.Import(context.Background(), uuid.New(), store, nil)
			if err != nil {
				t.Fatal(err)
			}
			if requests != 1 || stats.PagesFetched != 1 || stats.Fetched != 100 || stats.Normalized != 100 || stats.Inserted != 100 || stats.Skipped != 0 || stats.Errors != 0 || stats.PosterErrors != 0 || len(store.events) != 100 {
				t.Fatalf("requests=%d stats=%+v persisted=%d", requests, stats, len(store.events))
			}
			for index, event := range store.events {
				if event.ExternalID != strconv.Itoa(index+1) || event.Title != fmt.Sprintf("Event %d", index+1) || event.Description != "Kept" || event.Venue.Address != "Street" || len(event.Images) != 1 || !store.marked[index] {
					t.Fatalf("event %d lost valid data: %+v", index, event)
				}
				if index == 20 && !fallback {
					if event.PriceFromMinor != nil || event.PriceToMinor != nil {
						t.Fatalf("unknown price became known: %+v", event)
					}
					continue
				}
				wantMin, wantMax := int32(12_550), int32(25_000)
				if index == 20 {
					wantMin, wantMax = 40_000, 70_000
				}
				if event.PriceFromMinor == nil || event.PriceToMinor == nil || *event.PriceFromMinor != wantMin || *event.PriceToMinor != wantMax {
					t.Fatalf("event %d prices=%v..%v, want %d..%d", index, event.PriceFromMinor, event.PriceToMinor, wantMin, wantMax)
				}
			}
			if len(store.starts) != 1 || !store.starts[0].UpsertOnly || len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunSucceeded {
				t.Fatalf("sync lifecycle: starts=%+v finishes=%+v", store.starts, store.finishes)
			}
		})
	}
}

func TestNonstandardTicketsDoNotStopFollowingPages(t *testing.T) {
	var skips []string
	event := func(id int, tickets string) string {
		return strings.TrimSuffix(timepadImportEventWithoutTicketURL(id, "Санкт-Петербург"), "}") + `,"ticket_types":` + tickets + `}`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skip := r.URL.Query().Get("skip")
		skips = append(skips, skip)
		switch skip {
		case "0":
			fmt.Fprintf(w, `{"total":3,"values":[%s,%s]}`, event(1, `[{"price":100,"is_active":true}]`), event(2, `{"unexpected":true}`))
		case "2":
			fmt.Fprintf(w, `{"total":3,"values":[%s]}`, event(3, `[{"price":200,"is_active":true}]`))
		default:
			t.Errorf("unexpected skip=%q", skip)
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client := testClient(t, server)
	client.cityName = "Санкт-Петербург"
	client.upsertOnly = true
	client.now = func() time.Time { return time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC) }
	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := client.Import(context.Background(), uuid.New(), store, nil)
	if err != nil || stats.PagesFetched != 2 || stats.Fetched != 3 || stats.Inserted != 3 || stats.Skipped != 0 || stats.PosterErrors != 0 || fmt.Sprint(skips) != "[0 2]" || fmt.Sprint(store.persisted) != "[1 2 3]" {
		t.Fatalf("err=%v stats=%+v skips=%v persisted=%v", err, stats, skips, store.persisted)
	}
	if store.events[1].PriceFromMinor != nil || store.events[1].PriceToMinor != nil || store.events[2].PriceFromMinor == nil || *store.events[2].PriceFromMinor != 20000 {
		t.Fatalf("unknown or following event prices changed: %+v", store.events)
	}
	if len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunSucceeded || !store.starts[0].UpsertOnly {
		t.Fatalf("sync lifecycle: starts=%+v finishes=%+v", store.starts, store.finishes)
	}
}
