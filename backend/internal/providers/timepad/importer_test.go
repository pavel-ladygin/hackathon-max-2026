package timepad

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestImportPosterSecondUpsertPersistenceFailureCountsIngestionError(t *testing.T) {
	var calls int
	store := eventStoreFunc(func(context.Context, uuid.UUID, providers.NormalizedEvent) (providers.UpsertResult, error) {
		calls++
		if calls == 2 {
			return providers.UpsertResult{}, errors.New("poster upsert private database detail")
		}
		return providers.UpsertResult{EventID: uuid.New(), Inserted: true}, nil
	})
	posterStore := &importPosterStore{
		client: &Client{wait: func(context.Context) error { return nil }}, store: store,
		postersByCanonicalID: make(map[int64][]providers.NormalizedImage),
		resolveCanonicalID:   func(context.Context, int64, string) (int64, error) { return 7, nil },
		fetchEventPoster: func(context.Context, int64) ([]providers.NormalizedImage, error) {
			return []providers.NormalizedImage{{URL: "https://assets.test/poster.jpg", Role: "card"}}, nil
		},
	}
	ingestion, err := providers.NewIngestion(uuid.New(), posterStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	ticketURL := "https://timepad.ru/event/42/"
	if err := ingestion.Persist(context.Background(), providers.NormalizedEvent{Source: timepadSource, ExternalID: "42", TicketURL: &ticketURL}); err != nil {
		t.Fatal(err)
	}
	if stats := ingestion.Stats(); calls != 2 || stats.Normalized != 1 || stats.Errors != 1 || stats.Inserted != 0 {
		t.Fatalf("calls=%d stats=%+v, want second persistence failure counted", calls, stats)
	}
}

type eventStoreFunc func(context.Context, uuid.UUID, providers.NormalizedEvent) (providers.UpsertResult, error)

func (f eventStoreFunc) UpsertWithResult(ctx context.Context, cityID uuid.UUID, event providers.NormalizedEvent) (providers.UpsertResult, error) {
	return f(ctx, cityID, event)
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
			fmt.Fprint(w, `{"total":4,"values":[`+timepadImportEvent(1, "  мОСКвА ")+`,`+timepadImportEvent(2, "Москва")+`]}`)
		case "2":
			fmt.Fprint(w, `{"total":4,"values":[`+timepadImportEvent(3, "Москва")+`,`+timepadImportEvent(4, "Москва")+`]}`)
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
	if first != (ImportStats{HTTPAttempts: 2, InitialTotal: 4, FinalOffset: 4, Scope: "full", PagesFetched: 2, Fetched: 4, Matched: 4, Normalized: 4, Inserted: 4, InsideWindow: 4}) {
		t.Fatalf("first stats = %+v", first)
	}
	if got, want := fmt.Sprint(skips), "[0 2]"; got != want {
		t.Fatalf("skip sequence = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(store.persisted), "[1 2 3 4]"; got != want {
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
	if second != (ImportStats{HTTPAttempts: 2, InitialTotal: 4, FinalOffset: 4, Scope: "full", PagesFetched: 2, Fetched: 4, Matched: 4, Normalized: 4, Updated: 4, InsideWindow: 4}) {
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

	client := testClient(t, server)
	client.now = func() time.Time { return time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC) }
	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := client.Import(context.Background(), uuid.New(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SyncRunID == uuid.Nil {
		t.Fatal("import returned no sync run ID")
	}
	stats.SyncRunID = uuid.Nil
	if requests != 1 || stats != (ImportStats{HTTPAttempts: 1, InitialTotal: 1, FinalOffset: 1, Scope: "full", PagesFetched: 1, Fetched: 1, Matched: 1, Normalized: 1, Inserted: 1, InsideWindow: 1}) {
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
	client.pageSize = 4
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
	want := ImportStats{HTTPAttempts: 1, InitialTotal: 4, FinalOffset: 4, Scope: "full", PagesFetched: 1, Fetched: 4, Matched: 4, Normalized: 2, Inserted: 2, Skipped: 2, InsideWindow: 2, Rejections: providers.RejectionStats{BeforeWindow: 1, AfterWindow: 1}}
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
	client := testClient(t, server)
	client.pageSize = 1
	stats, err := client.ImportPages(context.Background(), uuid.New(), store, nil, 1)
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
	client.now = func() time.Time { return time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC) }
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

func TestImportConfiguredCityUsesMetadataAndPreservesInstant(t *testing.T) {
	cityID := uuid.New()
	startsAt := time.Date(2026, 10, 1, 7, 0, 0, 0, time.FixedZone("provider", 5*60*60))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"total":1,"values":[`+timepadImportEventAt(81, "Казань", startsAt)+`]}`)
	}))
	defer server.Close()
	client := testClient(t, server)
	client.cityID = cityID
	client.cityName = "Казань"
	client.timezone = "Asia/Yekaterinburg"
	client.now = func() time.Time { return startsAt.Add(-time.Second).UTC() }
	store := &memoryEventStore{seen: make(map[string]struct{})}
	if _, err := client.Import(context.Background(), cityID, store, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 || store.events[0].Timezone != "Asia/Yekaterinburg" || !store.events[0].StartsAt.Equal(startsAt) {
		t.Fatalf("event=%+v, want configured timezone and unchanged instant", store.events)
	}
}

func TestImportConfiguredCityRejectsWrongUUIDBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client := testClient(t, server)
	client.cityID = uuid.New()
	client.cityName = "Казань"
	client.timezone = "Asia/Yekaterinburg"
	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := client.Import(context.Background(), uuid.New(), store, nil)
	if err == nil || stats != (ImportStats{}) || requests != 0 || len(store.starts) != 0 {
		t.Fatalf("stats=%+v err=%v requests=%d starts=%+v", stats, err, requests, store.starts)
	}
}

func TestImportUsesProviderCityFilterForDifferentPageCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		city := request.URL.Query().Get("cities")
		total := 205
		if city == "Санкт-Петербург" {
			total = 140
		}
		skip, _ := strconv.Atoi(request.URL.Query().Get("skip"))
		count := min(100, total-skip)
		fmt.Fprint(w, timepadPage(total, skip+1, count, city))
	}))
	defer server.Close()

	for _, test := range []struct {
		city  string
		pages int
	}{{"Москва", 3}, {"Санкт-Петербург", 2}} {
		t.Run(test.city, func(t *testing.T) {
			client := testClient(t, server)
			client.pageSize, client.cityName = 100, test.city
			client.now = func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }
			stats, err := client.Import(context.Background(), uuid.New(), &memoryEventStore{seen: make(map[string]struct{})}, nil)
			if err != nil || stats.PagesFetched != test.pages || stats.Fetched != stats.InitialTotal {
				t.Fatalf("stats=%+v err=%v", stats, err)
			}
		})
	}
}

func TestImportFailsWithoutReconciliationOnPaginationCorruption(t *testing.T) {
	for _, test := range []struct {
		name string
		page func(skip string) string
	}{{
		name: "missing mandatory values",
		page: func(string) string { return `{"total":2}` },
	}, {
		name: "drifting total",
		page: func(skip string) string {
			if skip == "0" {
				return timepadPage(4, 1, 2, "Москва")
			}
			return timepadPage(3, 3, 1, "Москва")
		},
	}, {
		name: "repeated ID",
		page: func(skip string) string {
			if skip == "0" {
				return timepadPage(4, 1, 2, "Москва")
			}
			return timepadPage(4, 2, 2, "Москва")
		},
	}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, test.page(r.URL.Query().Get("skip"))) }))
			defer server.Close()
			client := testClient(t, server)
			client.pageSize = 2
			store := &memoryEventStore{seen: make(map[string]struct{})}
			_, err := client.Import(context.Background(), uuid.New(), store, nil)
			if providers.SyncErrorCode(err) != "provider_pagination_error" || len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunFailed {
				t.Fatalf("err=%v finishes=%+v", err, store.finishes)
			}
		})
	}
}

func TestImportLateHTTPFailureDoesNotReconcile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("skip") == "0" {
			fmt.Fprint(w, timepadPage(117, 1, 100, "Москва"))
			return
		}
		http.Error(w, "provider details must not leak", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := testClient(t, server)
	client.pageSize = 100
	client.waitRetry = func(context.Context, time.Duration) error { return nil }
	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := client.Import(context.Background(), uuid.New(), store, nil)
	if providers.SyncErrorCode(err) != "provider_http_500" || stats.PagesFetched != 1 || stats.Fetched != 100 || len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunFailed {
		t.Fatalf("stats=%+v err=%v finishes=%+v", stats, err, store.finishes)
	}
}

func TestImportHandles117PagesAndFailsSafelyOnPage117(t *testing.T) {
	for _, test := range []struct {
		name       string
		failLast   bool
		wantPages  int
		wantCode   string
		wantFinish providers.SyncRunState
	}{
		{name: "complete", wantPages: 117, wantFinish: providers.SyncRunSucceeded},
		{name: "page 117 failure", failLast: true, wantPages: 116, wantCode: "provider_http_500", wantFinish: providers.SyncRunFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests++
				skip, _ := strconv.Atoi(request.URL.Query().Get("skip"))
				if test.failLast && skip == 116 {
					http.Error(w, "private provider payload", http.StatusInternalServerError)
					return
				}
				fmt.Fprint(w, timepadNoPosterPage(117, skip+1, 1, "Москва"))
			}))
			defer server.Close()
			client := testClient(t, server)
			client.pageSize = 1
			client.now = func() time.Time { return time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC) }
			client.waitRetry = func(context.Context, time.Duration) error { return nil }
			store := &memoryEventStore{seen: make(map[string]struct{})}
			stats, err := client.Import(context.Background(), uuid.New(), store, nil)
			if providers.SyncErrorCode(err) != test.wantCode || stats.PagesFetched != test.wantPages || len(store.finishes) != 1 || store.finishes[0].State != test.wantFinish {
				t.Fatalf("requests=%d stats=%+v err=%v finishes=%+v", requests, stats, err, store.finishes)
			}
			if !test.failLast && (requests != 117 || stats.HTTPAttempts != 117 || stats.Fetched != 117) {
				t.Fatalf("requests=%d stats=%+v, want 117 completed pages", requests, stats)
			}
		})
	}
}

func TestImportConfiguredPageCapStopsWithoutReconciliation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests++
		skip, _ := strconv.Atoi(request.URL.Query().Get("skip"))
		fmt.Fprint(w, timepadNoPosterPage(3, skip+1, 1, "Москва"))
	}))
	defer server.Close()
	client := testClient(t, server)
	client.pageSize, client.maxPagesPerRun = 1, 2
	store := &memoryEventStore{seen: make(map[string]struct{})}
	stats, err := client.Import(context.Background(), uuid.New(), store, nil)
	if err != nil || requests != 2 || stats.PagesFetched != 2 || stats.Scope != "limited_upsert_only" || stats.LimitReason != "configured_page_cap" || len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunSucceeded {
		t.Fatalf("requests=%d stats=%+v err=%v finishes=%+v", requests, stats, err, store.finishes)
	}
}

func TestImportRejectsIgnoredCityFilterAndUnverifiedAliases(t *testing.T) {
	for _, city := range []string{"Санкт-Петербург", "СПб", "Санкт Петербург", ""} {
		t.Run(city, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("cities") != "Москва" {
					t.Error("missing server city filter")
				}
				fmt.Fprint(w, timepadPage(1, 1, 1, city))
			}))
			defer server.Close()
			client := testClient(t, server)
			store := &memoryEventStore{seen: map[string]struct{}{}}
			stats, err := client.Import(context.Background(), uuid.New(), store, nil)
			if providers.SyncErrorCode(err) != "provider_city_mismatch" || stats.Rejections.City != 1 || len(store.events) != 0 || store.finishes[0].State != providers.SyncRunFailed {
				t.Fatalf("city=%q stats=%+v err=%v", city, stats, err)
			}
		})
	}
}

func timepadPage(total, firstID, count int, city string) string {
	values := make([]string, 0, count)
	for id := firstID; id < firstID+count; id++ {
		values = append(values, timepadImportEvent(id, city))
	}
	return fmt.Sprintf(`{"total":%d,"values":[%s]}`, total, strings.Join(values, ","))
}

func timepadImportEvent(id int, city string) string {
	return timepadImportEventAt(id, city, time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC))
}

func timepadImportEventWithoutTicketURL(id int, city string) string {
	return strings.Replace(timepadImportEvent(id, city), fmt.Sprintf(`,"url":"https://timepad.ru/event/%d"`, id), "", 1)
}

func timepadNoPosterPage(total, firstID, count int, city string) string {
	values := make([]string, 0, count)
	for id := firstID; id < firstID+count; id++ {
		value := timepadImportEventWithoutTicketURL(id, city)
		value = strings.Replace(value, `,"poster_image":{"default_url":"https://assets.test/poster.jpg"}`, "", 1)
		values = append(values, value)
	}
	return fmt.Sprintf(`{"total":%d,"values":[%s]}`, total, strings.Join(values, ","))
}

func timepadImportEventAt(id int, city string, startsAt time.Time) string {
	return fmt.Sprintf(`{"id":%d,"name":"Event %d","starts_at":%q,"url":"https://timepad.ru/event/%d","poster_image":{"default_url":"https://assets.test/poster.jpg"},"location":{"city":%q,"address":"Street","coordinates":[55.75,37.61]},"registration_data":{"is_registration_open":true}}`, id, id, startsAt.Format(time.RFC3339), id, city)
}
