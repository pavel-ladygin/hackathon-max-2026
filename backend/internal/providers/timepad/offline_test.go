package timepad

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

var offlineBaseTransport http.RoundTripper = http.DefaultTransport

// TestMain blocks accidental public HTTP before any DNS lookup. Tests that
// need synthetic external hosts use roundTripFunc and therefore never reach
// this transport.
func TestMain(m *testing.M) {
	previous := http.DefaultTransport
	offlineBaseTransport = previous
	http.DefaultTransport = offlineOnlyTransport{base: previous}
	code := m.Run()
	http.DefaultTransport = previous
	os.Exit(code)
}

type offlineOnlyTransport struct{ base http.RoundTripper }

func (t offlineOnlyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	host := strings.TrimSuffix(strings.ToLower(request.URL.Hostname()), ".")
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("offline Timepad test blocked non-loopback HTTP host " + host)
		}
	}
	base := t.base
	if base == nil {
		base = offlineBaseTransport
	}
	return base.RoundTrip(request)
}

func TestOfflineTransportBlocksPublicHostBeforeDNS(t *testing.T) {
	_, err := (offlineOnlyTransport{}).RoundTrip(httptestRequest(t, "https://public.example.invalid/event"))
	if err == nil || !strings.Contains(err.Error(), "blocked non-loopback") {
		t.Fatalf("RoundTrip() error = %v, want offline block", err)
	}
}

func TestOfflineTransportAllowsLoopbackServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	client := testClient(t, server)
	response, err := client.httpClient.Get(server.URL)
	if err != nil {
		t.Fatalf("loopback server request failed: %v", err)
	}
	response.Body.Close()
}

func httptestRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
