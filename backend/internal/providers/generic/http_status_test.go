package generic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
)

func TestJSONFetchReportsActualSuccessfulStatus(t *testing.T) {
	server := newTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `[]`)
	})
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	resolve := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	var status int
	_, err := fetchJSONWithTLSConfigStatus(context.Background(), HTTPConfig{Endpoint: "https://events.example/data"}, resolve, dial, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, &status)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("status=%d err=%v", status, err)
	}
}
