package timepad

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	baseURL, err := url.Parse(server.URL + "/api/")
	if err != nil {
		t.Fatal(err)
	}
	return &Client{
		baseURL:    baseURL,
		token:      "test-token",
		httpClient: server.Client(),
		pageSize:   2,
		now:        time.Now,
		wait:       func(context.Context) error { return nil },
	}
}

func TestClientFormsRequestWithoutCitiesFilter(t *testing.T) {
	startsAtMin := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	startsAtMax := startsAtMin.Add(90 * 24 * time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/events.json" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
		}
		query := request.URL.Query()
		if _, exists := query["cities"]; exists {
			t.Errorf("cities must be omitted, got %q", query.Get("cities"))
		}
		for key, want := range map[string]string{
			"limit":         "2",
			"skip":          "4",
			"sort":          "+starts_at",
			"starts_at_min": startsAtMin.Format(time.RFC3339),
			"starts_at_max": startsAtMax.Format(time.RFC3339),
			"fields":        "location,registration_data,ticket_types",
		} {
			if got := query.Get(key); got != want {
				t.Errorf("query %s = %q, want %q", key, got, want)
			}
		}
		fmt.Fprint(w, `{"total":0,"values":[]}`)
	}))
	defer server.Close()

	if _, err := testClient(t, server).fetchPage(context.Background(), 4, startsAtMin, startsAtMax); err != nil {
		t.Fatal(err)
	}
}

func TestClientHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := testClient(t, server).fetchPage(ctx, 0, time.Now(), time.Now().Add(time.Hour))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestClientRateLimitErrorIsClear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := testClient(t, server).fetchPage(context.Background(), 0, time.Now(), time.Now().Add(time.Hour))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "rate limit") || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %v, want a clear rate-limit error", err)
	}
}
