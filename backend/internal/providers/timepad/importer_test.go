package timepad

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	events    []providers.NormalizedEvent
	starts    []providers.SyncRunStart
	finishes  []providers.SyncRunFinish
}

func (s *memoryEventStore) UpsertWithResult(_ context.Context, _ uuid.UUID, event providers.NormalizedEvent) (providers.UpsertResult, error) {
	s.marked = append(s.marked, event.ProviderLastSeenRunID != nil)
	s.events = append(s.events, event)
	_, exists := s.seen[event.ExternalID]
	s.seen[event.ExternalID] = struct{}{}
	s.persisted = append(s.persisted, event.ExternalID)
	return providers.UpsertResult{EventID: uuid.New(), Inserted: !exists}, nil
}

func TestImportParsesPosterImageReturnedByEventsList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !strings.Contains(","+request.URL.Query().Get("fields")+",", ",poster_image,") {
			t.Errorf("fields = %q, want poster_image", request.URL.Query().Get("fields"))
		}
		fmt.Fprint(w, `{"total":1,"values":[{"id":42,"name":"Event 42","starts_at":"2026-10-01T07:00:00Z","url":"https://timepad.ru/event/42","poster_image":{"default_url":"https://ucare.timepad.ru/poster.jpg"},"location":{"city":"Москва","address":"Street","coordinates":[55.75,37.61]},"registration_data":{"is_registration_open":true}}]}`)
	}))
	defer server.Close()

	client := testClient(t, server)
	client.now = func() time.Time { return time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC) }
	store := &memoryEventStore{seen: make(map[string]struct{})}
	if _, err := client.Import(context.Background(), uuid.New(), store, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 {
		t.Fatalf("persisted events = %d, want 1", len(store.events))
	}
	want := providers.NormalizedImage{URL: "https://ucare.timepad.ru/poster.jpg", Role: "card", Position: 0}
	if len(store.events[0].Images) != 1 || store.events[0].Images[0] != want {
		t.Fatalf("normalized images = %+v, want %+v", store.events[0].Images, []providers.NormalizedImage{want})
	}
}

func TestImportPosterStoreLooksUpOnlyNewEventsAndCachesCanonicalPoster(t *testing.T) {
	store := &memoryEventStore{seen: make(map[string]struct{})}
	client := &Client{wait: func(context.Context) error { return nil }}
	resolveCalls, fetchCalls := 0, 0
	poster := providers.NormalizedImage{URL: "https://ucare.timepad.ru/recovered.jpg", Role: "card", Position: 0}
	wrapper := &importPosterStore{
		client: client, store: store, postersByCanonicalID: make(map[int64][]providers.NormalizedImage),
		resolveCanonicalID: func(_ context.Context, id int64, _ string) (int64, error) {
			resolveCalls++
			return 900, nil
		},
		fetchEventPoster: func(_ context.Context, id int64) ([]providers.NormalizedImage, error) {
			fetchCalls++
			return []providers.NormalizedImage{poster}, nil
		},
	}
	cityID := uuid.New()
	for _, id := range []string{"42", "43"} {
		ticketURL := "https://timepad.ru/event/" + id
		event := providers.NormalizedEvent{Source: timepadSource, ExternalID: id, TicketURL: &ticketURL}
		result, err := wrapper.UpsertWithResult(context.Background(), cityID, event)
		if err != nil || !result.Inserted {
			t.Fatalf("first upsert result=%+v error=%v", result, err)
		}
	}
	if resolveCalls != 2 || fetchCalls != 1 {
		t.Fatalf("resolve calls=%d, fetch calls=%d, want 2 and 1", resolveCalls, fetchCalls)
	}
	if len(store.events) != 4 || len(store.events[1].Images) != 1 || store.events[1].Images[0] != poster || len(store.events[3].Images) != 1 || store.events[3].Images[0] != poster {
		t.Fatalf("upserted events = %+v; expected initial records and recovered poster", store.events)
	}
	ticketURL := "https://timepad.ru/event/42"
	_, err := wrapper.UpsertWithResult(context.Background(), cityID, providers.NormalizedEvent{
		Source: timepadSource, ExternalID: "42", TicketURL: &ticketURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 2 || fetchCalls != 1 {
		t.Fatalf("existing event triggered lookup: resolve calls=%d, fetch calls=%d", resolveCalls, fetchCalls)
	}
}

func TestImportPosterLookupFailureDoesNotFailInsertedEvent(t *testing.T) {
	store := &memoryEventStore{seen: make(map[string]struct{})}
	client := &Client{wait: func(context.Context) error { return nil }}
	var reported []error
	ticketURL := "https://timepad.ru/event/44"
	wrapper := &importPosterStore{
		client: client, store: store, reportError: func(err error) { reported = append(reported, err) },
		postersByCanonicalID: make(map[int64][]providers.NormalizedImage),
		resolveCanonicalID:   func(context.Context, int64, string) (int64, error) { return 0, fmt.Errorf("lookup failed") },
		fetchEventPoster: func(context.Context, int64) ([]providers.NormalizedImage, error) {
			t.Fatal("fetch should not run")
			return nil, nil
		},
	}
	result, err := wrapper.UpsertWithResult(context.Background(), uuid.New(), providers.NormalizedEvent{
		Source: timepadSource, ExternalID: "44", TicketURL: &ticketURL,
	})
	if err != nil || !result.Inserted {
		t.Fatalf("result=%+v error=%v, want successful insert", result, err)
	}
	if len(reported) != 1 || len(store.events) != 1 {
		t.Fatalf("reported errors=%v persisted events=%d", reported, len(store.events))
	}
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
	if first.SyncRunID == uuid.Nil {
		t.Fatal("first import returned no sync run ID")
	}
	first.SyncRunID = uuid.Nil
	if first != (ImportStats{PagesFetched: 2, Fetched: 4, Matched: 1, Normalized: 1, Inserted: 1, Skipped: 3, InsideWindow: 1, Rejections: providers.RejectionStats{City: 3}}) {
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
	if second.SyncRunID == uuid.Nil {
		t.Fatal("second import returned no sync run ID")
	}
	second.SyncRunID = uuid.Nil
	if second != (ImportStats{PagesFetched: 2, Fetched: 4, Matched: 1, Normalized: 1, Updated: 1, Skipped: 3, InsideWindow: 1, Rejections: providers.RejectionStats{City: 3}}) {
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
	if stats.SyncRunID == uuid.Nil {
		t.Fatal("import returned no sync run ID")
	}
	stats.SyncRunID = uuid.Nil
	if requests != 1 || stats != (ImportStats{PagesFetched: 1, Fetched: 1, Matched: 1, Normalized: 1, Inserted: 1, InsideWindow: 1}) {
		t.Fatalf("requests=%d stats=%+v", requests, stats)
	}
}

func TestImportPagesOneFetchesOnePageAndDoesNotReconcile(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		fmt.Fprint(w, `{"total":4,"values":[`+timepadImportEventWithoutTicketURL(31, "Москва")+`,`+timepadImportEventWithoutTicketURL(32, "Москва")+`]}`)
	}))
	defer server.Close()

	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := testClient(t, server).ImportPages(context.Background(), uuid.New(), store, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || stats.PagesFetched != 1 || stats.Fetched != 2 {
		t.Fatalf("requests=%d stats=%+v", requests, stats)
	}
	if len(store.starts) != 1 || !store.starts[0].UpsertOnly {
		t.Fatalf("sync starts=%+v, want one upsert-only run", store.starts)
	}
	if len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunSucceeded {
		t.Fatalf("sync finishes=%+v", store.finishes)
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
	if stats.SyncRunID == uuid.Nil {
		t.Fatal("import returned no sync run ID")
	}
	stats.SyncRunID = uuid.Nil
	want := ImportStats{PagesFetched: 1, Fetched: 4, Matched: 4, Normalized: 2, Inserted: 2, Skipped: 2, InsideWindow: 2, Rejections: providers.RejectionStats{BeforeWindow: 1, AfterWindow: 1}}
	if stats != want {
		t.Fatalf("stats = %+v", stats)
	}
	if got, want := fmt.Sprint(store.persisted), "[21 22]"; got != want {
		t.Fatalf("persisted = %s, want %s", got, want)
	}
	if clockCalls != 1 {
		t.Fatalf("clock calls = %d, want 1", clockCalls)
	}
}

func TestFullImportFailsWhenMatchedEventsCannotNormalize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"total":1,"values":[{"id":51,"name":"Broken","location":{"city":"Москва"}}]}`)
	}))
	defer server.Close()
	client := testClient(t, server)
	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := client.Import(context.Background(), uuid.New(), store, nil)
	if providers.SyncErrorCode(err) != "timepad_normalization_empty" {
		t.Fatalf("error=%v code=%q", err, providers.SyncErrorCode(err))
	}
	if stats.Matched != 1 || stats.Normalized != 0 || stats.Rejections.MissingStartsAt != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	if len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunFailed || store.finishes[0].ErrorText != "timepad_normalization_empty" {
		t.Fatalf("finishes=%+v", store.finishes)
	}
}

func TestPartialNormalizationEmptyRemainsSuccessfulUpsertOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"total":2,"values":[{"id":52,"name":"Broken","location":{"city":"Москва"}}]}`)
	}))
	defer server.Close()
	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := testClient(t, server).ImportPages(context.Background(), uuid.New(), store, nil, 1)
	if err != nil || stats.Rejections.MissingStartsAt != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	if len(store.starts) != 1 || !store.starts[0].UpsertOnly || len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunSucceeded {
		t.Fatalf("starts=%+v finishes=%+v", store.starts, store.finishes)
	}
}

func TestImportCancellationStopsPaginationWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"total":3,"values":[`+timepadImportEventWithoutTicketURL(10, "Москва")+`,`+timepadImportEventWithoutTicketURL(11, "Москва")+`]}`)
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
	if stats.SyncRunID == uuid.Nil {
		t.Fatal("cancelled import returned no sync run ID")
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

func timepadImportEventWithoutTicketURL(id int, city string) string {
	return strings.Replace(timepadImportEvent(id, city), fmt.Sprintf(`,"url":"https://timepad.ru/event/%d"`, id), "", 1)
}

func timepadImportEventAt(id int, city string, startsAt time.Time) string {
	return fmt.Sprintf(`{"id":%d,"name":"Event %d","starts_at":%q,"url":"https://timepad.ru/event/%d","location":{"city":%q,"address":"Street","coordinates":[55.75,37.61]},"registration_data":{"is_registration_open":true}}`, id, id, startsAt.Format(time.RFC3339), id, city)
}
