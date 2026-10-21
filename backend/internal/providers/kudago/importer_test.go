package kudago

import (
	"context"
	"errors"
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

type memoryEventStore struct {
	seen      map[string]struct{}
	fail      map[string]error
	persisted []string
	events    []providers.NormalizedEvent
	starts    []providers.SyncRunStart
	finishes  []providers.SyncRunFinish
}

func (s *memoryEventStore) UpsertWithResult(_ context.Context, _ uuid.UUID, event providers.NormalizedEvent) (providers.UpsertResult, error) {
	if event.ProviderLastSeenRunID == nil {
		return providers.UpsertResult{}, errors.New("missing sync run marker")
	}
	if err := s.fail[event.ExternalID]; err != nil {
		return providers.UpsertResult{}, err
	}
	_, exists := s.seen[event.ExternalID]
	s.seen[event.ExternalID] = struct{}{}
	s.persisted = append(s.persisted, event.ExternalID)
	s.events = append(s.events, event)
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

func TestImportPaginatesSkipsInvalidAndSupportsRerun(t *testing.T) {
	importNow := time.Unix(1_699_999_900, 0).UTC()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"count":4,"next":"","results":[`+validImportEvent(1, 1700000000)+`,`+validImportEvent(3, 1700000300)+`]}`)
			return
		}
		since, sinceErr := strconv.ParseInt(r.URL.Query().Get("actual_since"), 10, 64)
		until, untilErr := strconv.ParseInt(r.URL.Query().Get("actual_until"), 10, 64)
		if sinceErr != nil || untilErr != nil || since != importNow.Unix() || until != importNow.Add(importHorizon).Unix() {
			t.Errorf("import window actual_since=%q actual_until=%q", r.URL.Query().Get("actual_since"), r.URL.Query().Get("actual_until"))
		}
		fmt.Fprintf(w, `{"count":4,"next":%q,"results":[%s,{"id":2,"title":"invalid","dates":[{"start":1700000200}]}]}`,
			server.URL+"/events/?page=2", validImportEvent(1, 1700000000))
	}))
	defer server.Close()

	client, err := NewClient(Options{BaseURL: server.URL, Timeout: time.Second, Location: "msk", PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	nowCalls := 0
	client.now = func() time.Time {
		nowCalls++
		return importNow
	}
	store := &memoryEventStore{seen: make(map[string]struct{}), fail: make(map[string]error)}
	cityID := uuid.New()
	first, err := client.Import(context.Background(), cityID, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.SyncRunID == uuid.Nil {
		t.Fatal("first import returned no sync run ID")
	}
	first.SyncRunID = uuid.Nil
	if first != (ImportStats{PagesFetched: 2, Fetched: 4, Matched: 4, Normalized: 2, Inserted: 2, Skipped: 2, Rejections: providers.RejectionStats{Duplicate: 1}}) {
		t.Fatalf("first stats = %+v", first)
	}
	second, err := client.Import(context.Background(), cityID, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.SyncRunID == uuid.Nil {
		t.Fatal("second import returned no sync run ID")
	}
	second.SyncRunID = uuid.Nil
	if second != (ImportStats{PagesFetched: 2, Fetched: 4, Matched: 4, Normalized: 2, Updated: 2, Skipped: 2, Rejections: providers.RejectionStats{Duplicate: 1}}) {
		t.Fatalf("second stats = %+v", second)
	}
	if nowCalls != 2 {
		t.Fatalf("clock calls=%d, want one per import", nowCalls)
	}
	if len(store.starts) != 2 || len(store.finishes) != 2 || store.finishes[0].State != providers.SyncRunSucceeded || store.finishes[1].State != providers.SyncRunSucceeded {
		t.Fatalf("sync lifecycle starts=%+v finishes=%+v", store.starts, store.finishes)
	}
}

func TestImportContinuesAfterPersistenceError(t *testing.T) {
	importNow := time.Unix(1_699_999_900, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"count":2,"next":"","results":[`+validImportEvent(7, 1700000700)+`,`+validImportEvent(8, 1700000800)+`]}`)
	}))
	defer server.Close()
	client, err := NewClient(Options{BaseURL: server.URL, Timeout: time.Second, Location: "msk", PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return importNow }
	store := &memoryEventStore{seen: make(map[string]struct{}), fail: map[string]error{"7:1700000700": errors.New("database failure")}}
	var reported int
	stats, err := client.Import(context.Background(), uuid.New(), store, func(error) { reported++ })
	if err != nil {
		t.Fatal(err)
	}
	if stats.SyncRunID == uuid.Nil {
		t.Fatal("import returned no sync run ID")
	}
	stats.SyncRunID = uuid.Nil
	if stats != (ImportStats{PagesFetched: 1, Fetched: 2, Matched: 2, Normalized: 2, Inserted: 1, Errors: 1}) || reported != 1 || len(store.persisted) != 1 {
		t.Fatalf("stats=%+v reported=%d persisted=%v", stats, reported, store.persisted)
	}
	if len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunFailed {
		t.Fatalf("finish = %+v, want failed", store.finishes)
	}
}

func validImportEvent(id, start int64) string {
	return fmt.Sprintf(`{"id":%d,"title":"Event %d","dates":[{"start":%d}],"categories":["concert"],"site_url":"https://kudago.com/msk/event/%d/","place":{"id":%d,"title":"Venue","address":"Street","coords":{"lat":55.75,"lon":37.61}}}`, id, id, start, id, id)
}

func TestImportConfiguredCityUsesTimezoneWithoutChangingInstant(t *testing.T) {
	const timestamp = int64(1_700_000_000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"count":1,"next":"","results":[`+withLocation(validImportEvent(70, timestamp), "kzn")+`]}`)
	}))
	defer server.Close()
	cityID := uuid.New()
	client, err := NewClient(Options{BaseURL: server.URL, Timeout: time.Second, Location: "kzn", PageSize: 100, CityID: cityID, Timezone: "Asia/Yekaterinburg"})
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return time.Unix(timestamp-1, 0).UTC() }
	store := &memoryEventStore{seen: make(map[string]struct{}), fail: make(map[string]error)}
	if _, err := client.Import(context.Background(), cityID, store, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 || store.events[0].Timezone != "Asia/Yekaterinburg" || store.events[0].StartsAt.Unix() != timestamp {
		t.Fatalf("event=%+v, want configured timezone and unchanged instant", store.events)
	}
}

func TestImportConfiguredCityRejectsMismatchedLocationBeforeWrites(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		fmt.Fprint(w, `{"count":1,"next":"","results":[`+withLocation(validImportEvent(71, 1_700_000_000), "msk")+`]}`)
	}))
	defer server.Close()
	cityID := uuid.New()
	client, err := NewClient(Options{BaseURL: server.URL, Timeout: time.Second, Location: "kzn", PageSize: 100, CityID: cityID, Timezone: "Asia/Yekaterinburg"})
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return time.Unix(1_699_999_900, 0).UTC() }
	store := &memoryEventStore{seen: make(map[string]struct{}), fail: make(map[string]error)}
	stats, err := client.Import(context.Background(), cityID, store, nil)
	if err == nil || stats.Rejections.City != 1 || len(store.persisted) != 0 || len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunFailed {
		t.Fatalf("requests=%d stats=%+v err=%v persisted=%v finishes=%+v", requests, stats, err, store.persisted, store.finishes)
	}
	if _, err := client.Import(context.Background(), uuid.New(), store, nil); err == nil || requests != 1 {
		t.Fatalf("configured UUID mismatch must fail before network: requests=%d err=%v", requests, err)
	}
}

func withLocation(event, slug string) string {
	return strings.Replace(event, `,"place":`, `,"location":{"slug":"`+slug+`"},"place":`, 1)
}
