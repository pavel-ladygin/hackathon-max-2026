package timepad

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type memoryEventStore struct {
	seen      map[string]struct{}
	persisted []string
	marked    []bool
	starts    []providers.SyncRunStart
	finishes  []providers.SyncRunFinish
}

func (s *memoryEventStore) UpsertWithResult(_ context.Context, _ uuid.UUID, event providers.NormalizedEvent) (providers.UpsertResult, error) {
	s.marked = append(s.marked, event.ProviderLastSeenRunID != nil)
	_, exists := s.seen[event.ExternalID]
	s.seen[event.ExternalID] = struct{}{}
	s.persisted = append(s.persisted, event.ExternalID)
	return providers.UpsertResult{EventID: uuid.New(), Inserted: !exists}, nil
}

func (s *memoryEventStore) BeginSyncRun(_ context.Context, start providers.SyncRunStart) (uuid.UUID, error) {
	s.starts = append(s.starts, start)
	return uuid.New(), nil
}

func (s *memoryEventStore) FinishSyncRun(_ context.Context, finish providers.SyncRunFinish) (int, error) {
	s.finishes = append(s.finishes, finish)
	return 0, nil
}

func TestImportFiltersCityPaginatesSequentiallyAndSupportsRerun(t *testing.T) {
	var active int32
	var skips []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if atomic.AddInt32(&active, 1) != 1 {
			t.Error("Timepad pages must not be fetched concurrently")
		}
		defer atomic.AddInt32(&active, -1)
		skip := request.URL.Query().Get("skip")
		skips = append(skips, skip)
		switch skip {
		case "0":
			fmt.Fprint(w, `{"total":4,"values":[`+timepadImportEvent(1, "  мОСКвА ")+`,`+timepadImportEvent(2, "Онлайн")+`]}`)
		case "2":
			fmt.Fprint(w, `{"total":4,"values":[`+timepadImportEvent(3, "Санкт-Петербург")+`,`+timepadImportEvent(4, "")+`]}`)
		default:
			t.Errorf("unexpected skip %q", skip)
		}
	}))
	defer server.Close()

	client := testClient(t, server)
	client.now = func() time.Time { return time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC) }
	store := &memoryEventStore{seen: make(map[string]struct{})}
	cityID := uuid.New()

	first, err := client.Import(context.Background(), cityID, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != (ImportStats{PagesFetched: 2, Fetched: 4, Matched: 1, Normalized: 1, Inserted: 1, Skipped: 3}) {
		t.Fatalf("first stats = %+v", first)
	}
	if got, want := fmt.Sprint(skips), "[0 2]"; got != want {
		t.Fatalf("skip sequence = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(store.persisted), "[1]"; got != want {
		t.Fatalf("persisted = %s, want %s", got, want)
	}

	skips = nil
	second, err := client.Import(context.Background(), cityID, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second != (ImportStats{PagesFetched: 2, Fetched: 4, Matched: 1, Normalized: 1, Updated: 1, Skipped: 3}) {
		t.Fatalf("second stats = %+v", second)
	}
	if got, want := fmt.Sprint(skips), "[0 2]"; got != want {
		t.Fatalf("rerun skip sequence = %s, want %s", got, want)
	}
	if len(store.starts) != 2 || len(store.finishes) != 2 || store.finishes[0].State != providers.SyncRunSucceeded || store.finishes[1].State != providers.SyncRunSucceeded {
		t.Fatalf("sync lifecycle starts=%+v finishes=%+v", store.starts, store.finishes)
	}
	for index, marked := range store.marked {
		if !marked {
			t.Fatalf("persisted event %d has no sync run marker", index)
		}
	}
}

func TestImportStopsWhenPageTotalIsReached(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		if got := request.URL.Query().Get("skip"); got != "0" {
			t.Errorf("skip = %q, want 0", got)
		}
		fmt.Fprint(w, `{"total":1,"values":[`+timepadImportEvent(9, "Москва")+`]}`)
	}))
	defer server.Close()

	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := testClient(t, server).Import(context.Background(), uuid.New(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || stats != (ImportStats{PagesFetched: 1, Fetched: 1, Matched: 1, Normalized: 1, Inserted: 1}) {
		t.Fatalf("requests=%d stats=%+v", requests, stats)
	}
}

func TestImportFiltersNormalizedEventsOutsideRequestedWindow(t *testing.T) {
	startsAtMin := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	startsAtMax := startsAtMin.Add(importHorizon)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"total":4,"values":[%s,%s,%s,%s]}`,
			timepadImportEventAt(20, "Москва", startsAtMin.Add(-time.Second)),
			timepadImportEventAt(21, "Москва", startsAtMin),
			timepadImportEventAt(22, "Москва", startsAtMax),
			timepadImportEventAt(23, "Москва", startsAtMax.Add(time.Second)),
		)
	}))
	defer server.Close()

	client := testClient(t, server)
	clockCalls := 0
	client.now = func() time.Time {
		clockCalls++
		return startsAtMin
	}
	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := client.Import(context.Background(), uuid.New(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats != (ImportStats{PagesFetched: 1, Fetched: 4, Matched: 4, Normalized: 2, Inserted: 2, Skipped: 2}) {
		t.Fatalf("stats = %+v", stats)
	}
	if got, want := fmt.Sprint(store.persisted), "[21 22]"; got != want {
		t.Fatalf("persisted = %s, want %s", got, want)
	}
	if clockCalls != 1 {
		t.Fatalf("clock calls = %d, want 1", clockCalls)
	}
}

func TestImportCancellationStopsPaginationWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"total":3,"values":[`+timepadImportEvent(10, "Москва")+`,`+timepadImportEvent(11, "Москва")+`]}`)
	}))
	defer server.Close()

	client := testClient(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	client.wait = func(waitCtx context.Context) error {
		cancel()
		<-waitCtx.Done()
		return waitCtx.Err()
	}
	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := client.Import(ctx, uuid.New(), store, nil)
	if err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if stats.PagesFetched != 1 || stats.Fetched != 2 || stats.Inserted != 2 {
		t.Fatalf("stats before cancellation = %+v", stats)
	}
	if len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunCancelled {
		t.Fatalf("finish = %+v, want cancelled", store.finishes)
	}
}

func timepadImportEvent(id int, city string) string {
	return timepadImportEventAt(id, city, time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC))
}

func timepadImportEventAt(id int, city string, startsAt time.Time) string {
	return fmt.Sprintf(`{"id":%d,"name":"Event %d","starts_at":%q,"url":"https://timepad.ru/event/%d","location":{"city":%q,"address":"Street","coordinates":[55.75,37.61]},"registration_data":{"is_registration_open":true}}`, id, id, startsAt.Format(time.RFC3339), id, city)
}
