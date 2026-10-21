package kudago

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type countingLimiter struct{ calls int }

func (l *countingLimiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.calls++
	return nil
}

func TestKudaGoRetryPolicyCountsEveryAttempt(t *testing.T) {
	for _, test := range []struct {
		status, attempts int
		retry            string
	}{{401, 1, ""}, {403, 1, ""}, {429, 2, "1"}, {429, 1, "999999999999999999"}, {503, 3, ""}} {
		t.Run(fmt.Sprint(test.status, test.retry), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", test.retry)
				http.Error(w, "secret-provider-body", test.status)
			}))
			defer server.Close()
			client := testClient(t, server, time.Second)
			limiter := &countingLimiter{}
			client.limiter = limiter
			var delays []time.Duration
			client.waitRetry = func(_ context.Context, delay time.Duration) error { delays = append(delays, delay); return nil }
			_, attempts, err := client.fetchPageWithAttempts(context.Background(), fetchOptions{})
			if err == nil || calls != test.attempts || attempts != calls || limiter.calls != calls || strings.Contains(err.Error(), "secret-provider-body") {
				t.Fatalf("calls=%d attempts=%d permits=%d err=%v", calls, attempts, limiter.calls, err)
			}
			if test.status == 429 && calls == 2 && (len(delays) != 1 || delays[0] < time.Second) {
				t.Fatalf("Retry-After shortened: %v", delays)
			}
		})
	}
}

func TestKudaGoLimitedRunAndMalformedCompletion(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		cap           int
		wantCode      string
	}{
		{"configured cap", `{"count":2,"next":"?page=2","results":[EVENT]}`, 1, ""},
		{"lost next at cap", `{"count":2,"results":[EVENT]}`, 1, "provider_pagination_error"},
		{"lost next", `{"count":2,"results":[EVENT]}`, 0, "provider_pagination_error"},
		{"missing results", `{"count":0}`, 0, "provider_pagination_error"},
		{"missing count", `{"results":[]}`, 0, "provider_pagination_error"},
		{"null results", `{"count":0,"results":null}`, 0, "provider_pagination_error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				fmt.Fprint(w, strings.Replace(test.payload, "EVENT", validImportEvent(501, 1_700_000_000), 1))
			}))
			defer server.Close()
			client := testClient(t, server, time.Second)
			client.now = func() time.Time { return time.Unix(1_699_999_999, 0) }
			client.upsertOnly = true
			client.maxPages = test.cap
			store := &memoryEventStore{seen: map[string]struct{}{}, fail: map[string]error{}}
			stats, err := client.ImportPages(context.Background(), uuid.New(), store, nil, 0)
			if providers.SyncErrorCode(err) != test.wantCode || requests != 1 {
				t.Fatalf("stats=%+v err=%v requests=%d", stats, err, requests)
			}
			if !store.starts[0].UpsertOnly || stats.Reconciled != 0 {
				t.Fatal("limited run allowed reconciliation")
			}
		})
	}
}

func TestKudaGoRunDeadlineFinalizesFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := testClient(t, server, time.Second)
	client.maxDuration = 20 * time.Millisecond
	store := &memoryEventStore{seen: map[string]struct{}{}, fail: map[string]error{}}
	stats, err := client.Import(context.Background(), uuid.New(), store, nil)
	if providers.SyncErrorCode(err) != "provider_timeout" || len(store.finishes) != 1 || store.finishes[0].State != providers.SyncRunFailed || stats.Reconciled != 0 {
		t.Fatalf("stats=%+v err=%v finish=%+v", stats, err, store.finishes)
	}
}
