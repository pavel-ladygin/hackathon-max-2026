package timepad

import (
	"errors"
	"net"
	"net/http"
	"os"
	"testing"
)

// The real transport is reachable only for a literal loopback mock address.
// Synthetic provider-host tests use their own in-memory RoundTripper instead.
type mockNetworkOnly struct{ next http.RoundTripper }

func (transport mockNetworkOnly) RoundTrip(request *http.Request) (*http.Response, error) {
	address := net.ParseIP(request.URL.Hostname())
	if address == nil || !address.IsLoopback() {
		return nil, errors.New("offline test forbids external HTTP")
	}
	return transport.next.RoundTrip(request)
}

func TestMain(m *testing.M) {
	http.DefaultTransport = mockNetworkOnly{next: http.DefaultTransport}
	os.Exit(m.Run())
}
