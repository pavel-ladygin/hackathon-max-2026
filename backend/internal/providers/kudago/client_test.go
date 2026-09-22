package kudago

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

func testClient(t *testing.T, server *httptest.Server, timeout time.Duration) *Client {
	t.Helper()
	client, err := NewClient(Options{BaseURL: server.URL + "/public-api/v1.4", Timeout: timeout, Location: "msk", PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientFetchPageSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count":1,"next":"","results":[{"id":42,"publication_date":1700000000,"title":"Event","short_title":"Short","tagline":"Tag","description":"Description","body_text":"Body","dates":[{"start":1700000100,"end":1700000200}],"categories":["concert"],"age_restriction":"18+","price":"1000 руб.","is_free":false,"images":[{"image":"https://img.example/event.jpg"}],"site_url":"https://kudago.com/msk/event/example/","location":{"slug":"msk"},"place":{"id":7,"title":"Venue","address":"Street 1","coords":{"lat":55.75,"lon":37.61},"subway":"Metro","categories":["concert-hall"]}}]}`)
	}))
	defer server.Close()

	page, err := testClient(t, server, time.Second).fetchPage(context.Background(), fetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 1 || len(page.Results) != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
	event := page.Results[0]
	if event.ID != 42 || event.Title != "Event" || len(event.Dates) != 1 || event.Dates[0].End == nil || *event.Dates[0].End != 1700000200 {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Place == nil || event.Place.Coords.Lat == nil || event.Place.Coords.Lon == nil || *event.Place.Coords.Lat != 55.75 || *event.Place.Coords.Lon != 37.61 {
		t.Fatalf("unexpected place: %+v", event.Place)
	}
}

func TestClientAcceptsNumericAgeRestriction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"count":1,"next":"","results":[{"id":42,"age_restriction":18}]}`)
	}))
	defer server.Close()

	page, err := testClient(t, server, time.Second).fetchPage(context.Background(), fetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(page.Results[0].AgeRestriction); got != "18" {
		t.Fatalf("age restriction = %q, want 18", got)
	}
}

func TestClientPagination(t *testing.T) {
	requests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"count":2,"next":"","results":[{"id":2}]}`)
			return
		}
		fmt.Fprintf(w, `{"count":2,"next":%q,"results":[{"id":1}]}`, server.URL+"/public-api/v1.4/events/?page=2")
	}))
	defer server.Close()

	client := testClient(t, server, time.Second)
	first, err := client.fetchPage(context.Background(), fetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.fetchPage(context.Background(), fetchOptions{Cursor: first.Next})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(second.Results) != 1 || second.Results[0].ID != 2 {
		t.Fatalf("requests=%d second=%+v", requests, second)
	}
}

func TestClientTimeoutAndContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
			fmt.Fprint(w, `{"count":0,"results":[]}`)
		}
	}))
	defer server.Close()

	t.Run("client timeout", func(t *testing.T) {
		_, err := testClient(t, server, 20*time.Millisecond).fetchPage(context.Background(), fetchOptions{})
		if err == nil || !strings.Contains(err.Error(), "perform kudago request") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := testClient(t, server, time.Second).fetchPage(ctx, fetchOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v, want context.Canceled", err)
		}
	})
}

func TestClientHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "provider unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := testClient(t, server, time.Second).fetchPage(context.Background(), fetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "status 500") || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientBoundsHTTPErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, strings.Repeat("x", maxErrorBodyBytes)+"secret-tail")
	}))
	defer server.Close()

	_, err := testClient(t, server, time.Second).fetchPage(context.Background(), fetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "[truncated]") || strings.Contains(err.Error(), "secret-tail") {
		t.Fatalf("unexpected bounded error: %v", err)
	}
	if len(err.Error()) > maxErrorBodyBytes+100 {
		t.Fatalf("error is not bounded: %d bytes", len(err.Error()))
	}
}

func TestClientRejectsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[`)
	}))
	defer server.Close()

	_, err := testClient(t, server, time.Second).fetchPage(context.Background(), fetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "decode kudago response") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientAcceptsEmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
	}))
	defer server.Close()

	page, err := testClient(t, server, time.Second).fetchPage(context.Background(), fetchOptions{})
	if err != nil || page.Count != 0 || len(page.Results) != 0 || page.Next != "" {
		t.Fatalf("page=%+v error=%v", page, err)
	}
}

func TestClientFormsExpectedRequest(t *testing.T) {
	from := time.Unix(1_700_000_000, 0)
	until := time.Unix(1_700_086_400, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/public-api/v1.4/events/" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request: %s %s headers=%v", r.Method, r.URL.Path, r.Header)
		}
		query := r.URL.Query()
		for key, want := range map[string]string{
			"lang": "ru", "location": "msk", "page_size": "50", "fields": requestedFields,
			"expand": requestedExpand, "text_format": "text", "actual_since": "1700000000", "actual_until": "1700086400",
		} {
			if got := query.Get(key); got != want {
				t.Errorf("query %s=%q, want %q", key, got, want)
			}
		}
		fmt.Fprint(w, `{"count":0,"results":[]}`)
	}))
	defer server.Close()

	_, err := testClient(t, server, time.Second).fetchPage(context.Background(), fetchOptions{ActualSince: from, ActualUntil: until})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsCrossOriginPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	client := testClient(t, server, time.Second)
	_, err := client.fetchPage(context.Background(), fetchOptions{Cursor: "https://attacker.example/events/?page=2"})
	if err == nil || !strings.Contains(err.Error(), "unexpected origin") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, parseErr := url.Parse(server.URL); parseErr != nil {
		t.Fatal(parseErr)
	}
}
