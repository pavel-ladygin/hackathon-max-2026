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
}

func (s *memoryEventStore) UpsertWithResult(_ context.Context, _ uuid.UUID, event providers.NormalizedEvent) (providers.UpsertResult, error) {
	_, exists := s.seen[event.ExternalID]
	s.seen[event.ExternalID] = struct{}{}
	s.persisted = append(s.persisted, event.ExternalID)
	return providers.UpsertResult{EventID: uuid.New(), Inserted: !exists}, nil
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
	stats, err := client.Import(ctx, uuid.New(), &memoryEventStore{seen: make(map[string]struct{})}, nil)
	if err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if stats.PagesFetched != 1 || stats.Fetched != 2 || stats.Inserted != 2 {
		t.Fatalf("stats before cancellation = %+v", stats)
	}
}

func timepadImportEvent(id int, city string) string {
	return fmt.Sprintf(`{"id":%d,"name":"Event %d","starts_at":"2026-10-01T10:00:00+0300","url":"https://timepad.ru/event/%d","location":{"city":%q,"address":"Street","coordinates":[55.75,37.61]},"registration_data":{"is_registration_open":true}}`, id, id, id, city)
}
