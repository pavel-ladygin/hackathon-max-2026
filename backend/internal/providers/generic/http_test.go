package generic

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateEndpointRejectsUnsafeURLs(t *testing.T) {
	tests := []struct{ name, endpoint string }{
		{"http", "http://example.com/events"},
		{"localhost", "https://localhost/events"},
		{"subdomain localhost", "https://api.localhost/events"},
		{"local domain", "https://events.local/events"},
		{"private IPv4", "https://10.1.2.3/events"},
		{"loopback IPv6", "https://[::1]/events"},
		{"userinfo", "https://user:password@example.com/events"},
		{"fragment", "https://example.com/events#secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := validateEndpoint(tt.endpoint); err == nil {
				t.Fatalf("validateEndpoint(%q) succeeded", tt.endpoint)
			}
		})
	}
	if _, err := validateEndpoint("https://example.com:8443/events"); err != nil {
		t.Fatalf("public HTTPS endpoint with custom port rejected: %v", err)
	}
}

func TestFetchJSONRejectsPrivateAndMixedDNSResultsBeforeDial(t *testing.T) {
	for name, ips := range map[string][]netip.Addr{
		"private": {netip.MustParseAddr("10.0.0.4")},
		"mixed":   {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
	} {
		t.Run(name, func(t *testing.T) {
			var dials atomic.Int32
			resolve := func(context.Context, string) ([]netip.Addr, error) { return ips, nil }
			dial := func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, errors.New("unexpected dial")
			}
			_, err := fetchJSONWithDependencies(context.Background(), HTTPConfig{Endpoint: "https://events.example/data"}, resolve, dial)
			if err == nil || dials.Load() != 0 {
				t.Fatalf("error=%v, dial calls=%d; want rejection before dial", err, dials.Load())
			}
		})
	}
}

func TestFetchJSONAuthQueryAndNumericValidatedDial(t *testing.T) {
	for _, mode := range []AuthMode{AuthNone, AuthBearer, AuthAPIKeyHeader, AuthAPIKeyQuery} {
		t.Run(string(mode), func(t *testing.T) {
			var dialAddress string
			server := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("fixed") != "value" || r.URL.Query().Get("existing") != "kept" {
					t.Errorf("query = %v", r.URL.Query())
				}
				switch mode {
				case AuthBearer:
					if r.Header.Get("Authorization") != "Bearer token-value" {
						t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
					}
				case AuthAPIKeyHeader:
					if r.Header.Get("X-API-Key") != "key-value" {
						t.Errorf("X-API-Key = %q", r.Header.Get("X-API-Key"))
					}
				case AuthAPIKeyQuery:
					if r.URL.Query().Get("access_key") != "key-value" {
						t.Errorf("query API key missing: %v", r.URL.Query())
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"large":900719925474099312345,"ok":true}`)
			})
			defer server.Close()
			config := HTTPConfig{Endpoint: "https://events.example/data?existing=kept", Query: url.Values{"fixed": {"value"}}}
			switch mode {
			case AuthBearer:
				config.Auth = AuthConfig{Mode: mode, Token: "token-value"}
			case AuthAPIKeyHeader:
				config.Auth = AuthConfig{Mode: mode, HeaderName: "X-API-Key", Key: "key-value"}
			case AuthAPIKeyQuery:
				config.Auth = AuthConfig{Mode: mode, QueryName: "access_key", Key: "key-value"}
			}
			resolve := func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}
			dial := func(ctx context.Context, network, address string) (net.Conn, error) {
				dialAddress = address
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			got, err := fetchJSONWithTLSConfig(context.Background(), config, resolve, dial, server.Client().Transport.(*http.Transport).TLSClientConfig)
			if err != nil {
				t.Fatalf("fetchJSON: %v", err)
			}
			if dialAddress != "93.184.216.34:443" {
				t.Errorf("dial address = %q, want checked numeric address", dialAddress)
			}
			object, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("result type %T", got)
			}
			if _, ok := object["large"].(json.Number); !ok {
				t.Errorf("large number type = %T", object["large"])
			}
		})
	}
}

func TestFetchJSONRejectsRedirectAndSanitizesHTTPError(t *testing.T) {
	const secret = "query-secret-should-not-leak"
	server := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://events.example/final?token="+secret, http.StatusFound)
			return
		}
		http.Error(w, "body contains "+secret, http.StatusBadGateway)
	})
	defer server.Close()
	for _, path := range []string{"/redirect", "/failure"} {
		t.Run(path, func(t *testing.T) {
			endpoint := "https://events.example" + path + "?api_key=" + secret
			_, err := fetchTestServer(t, server, HTTPConfig{Endpoint: endpoint})
			if err == nil {
				t.Fatal("expected HTTP error")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "api_key=") || strings.Contains(err.Error(), "/failure") {
				t.Fatalf("error leaked endpoint details: %v", err)
			}
		})
	}
}

func TestFetchJSONBodyValidationAndLimit(t *testing.T) {
	cases := []struct {
		name, body string
		tooLarge   bool
	}{
		{"malformed", `{broken`, false},
		{"trailing value", `{} {}`, false},
		{"oversized", strings.Repeat("x", HTTPMaxResponseBytes+1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newTLSServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.body) })
			defer server.Close()
			_, err := fetchTestServer(t, server, HTTPConfig{Endpoint: "https://events.example/data"})
			if err == nil {
				t.Fatal("expected response validation error")
			}
			if tc.tooLarge && !strings.Contains(err.Error(), "size") {
				t.Errorf("error %q does not identify size limit", err)
			}
		})
	}
}

func TestFetchJSONHonorsContextTimeout(t *testing.T) {
	server := newTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, `{}`)
	})
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := fetchTestServerContext(t, ctx, server, HTTPConfig{Endpoint: "https://events.example/slow?token=private"})
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "token=") {
		t.Fatalf("error=%v, want sanitized timeout", err)
	}
}

func TestFetchJSONVerifiesTLSHostname(t *testing.T) {
	server := newTLSServerWithName(t, "other.example", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) })
	defer server.Close()
	_, err := fetchTestServer(t, server, HTTPConfig{Endpoint: "https://events.example/data"})
	if err == nil || strings.Contains(err.Error(), "events.example") {
		t.Fatalf("hostname mismatch error = %v; want sanitized TLS verification failure", err)
	}
}

func newTLSServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	return newTLSServerWithName(t, "events.example", handler)
}

func newTLSServerWithName(t *testing.T, hostname string, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: hostname},
		DNSNames: []string{hostname}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	return server
}

func fetchTestServer(t *testing.T, server *httptest.Server, config HTTPConfig) (any, error) {
	t.Helper()
	return fetchTestServerContext(t, context.Background(), server, config)
}

func fetchTestServerContext(t *testing.T, ctx context.Context, server *httptest.Server, config HTTPConfig) (any, error) {
	t.Helper()
	cert, err := x509.ParseCertificate(server.Certificate().Raw)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	resolve := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return fetchJSONWithTLSConfig(ctx, config, resolve, dial, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
}
