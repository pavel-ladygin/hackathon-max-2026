package kudago

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type memoryEventStore struct {
	seen      map[string]struct{}
	fail      map[string]error
	persisted []string
}

func (s *memoryEventStore) UpsertWithResult(_ context.Context, _ uuid.UUID, event providers.NormalizedEvent) (providers.UpsertResult, error) {
	if err := s.fail[event.ExternalID]; err != nil {
		return providers.UpsertResult{}, err
	}
	_, exists := s.seen[event.ExternalID]
	s.seen[event.ExternalID] = struct{}{}
	s.persisted = append(s.persisted, event.ExternalID)
	return providers.UpsertResult{EventID: uuid.New(), Inserted: !exists}, nil
}

func TestImportPaginatesSkipsInvalidAndSupportsRerun(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"count":4,"next":"","results":[`+validImportEvent(1, 1700000000)+`,`+validImportEvent(3, 1700000300)+`]}`)
			return
		}
		since, sinceErr := strconv.ParseInt(r.URL.Query().Get("actual_since"), 10, 64)
		until, untilErr := strconv.ParseInt(r.URL.Query().Get("actual_until"), 10, 64)
		if sinceErr != nil || untilErr != nil || until-since != int64(importHorizon/time.Second) {
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
	store := &memoryEventStore{seen: make(map[string]struct{}), fail: make(map[string]error)}
	cityID := uuid.New()
	first, err := client.Import(context.Background(), cityID, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != (ImportStats{PagesFetched: 2, Fetched: 4, Matched: 4, Normalized: 2, Inserted: 2, Skipped: 2}) {
		t.Fatalf("first stats = %+v", first)
	}
	second, err := client.Import(context.Background(), cityID, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second != (ImportStats{PagesFetched: 2, Fetched: 4, Matched: 4, Normalized: 2, Updated: 2, Skipped: 2}) {
		t.Fatalf("second stats = %+v", second)
	}
}

func TestImportContinuesAfterPersistenceError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"count":2,"next":"","results":[`+validImportEvent(7, 1700000700)+`,`+validImportEvent(8, 1700000800)+`]}`)
	}))
	defer server.Close()
	client, err := NewClient(Options{BaseURL: server.URL, Timeout: time.Second, Location: "msk", PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryEventStore{seen: make(map[string]struct{}), fail: map[string]error{"7:1700000700": errors.New("database failure")}}
	var reported int
	stats, err := client.Import(context.Background(), uuid.New(), store, func(error) { reported++ })
	if err != nil {
		t.Fatal(err)
	}
	if stats != (ImportStats{PagesFetched: 1, Fetched: 2, Matched: 2, Normalized: 2, Inserted: 1, Errors: 1}) || reported != 1 || len(store.persisted) != 1 {
		t.Fatalf("stats=%+v reported=%d persisted=%v", stats, reported, store.persisted)
	}
}

func validImportEvent(id, start int64) string {
	return fmt.Sprintf(`{"id":%d,"title":"Event %d","dates":[{"start":%d}],"categories":["concert"],"site_url":"https://kudago.com/msk/event/%d/","place":{"id":%d,"title":"Venue","address":"Street","coords":{"lat":55.75,"lon":37.61}}}`, id, id, start, id, id)
}
