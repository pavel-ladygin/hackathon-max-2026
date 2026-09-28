package generic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func imageTestFetch(t *testing.T, server *httptestServer, ctx context.Context, timeout time.Duration) ([]byte, string, error) {
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
	return fetchImageWithTimeout(ctx, "https://events.example/picture", resolve, dial, &tls.Config{RootCAs: roots}, timeout)
}

// Alias keeps helper signatures readable while using httptest's concrete type.
type httptestServer = httptest.Server

func TestFetchImageValidatesSignatureAndContentType(t *testing.T) {
	tests := []struct {
		name, typ string
		body      []byte
		wantErr   bool
	}{
		{"jpeg", "image/jpeg", []byte{0xff, 0xd8, 0xff, 0}, false},
		{"png", "image/png", []byte("\x89PNG\r\n\x1a\nrest"), false},
		{"webp", "image/webp", []byte("RIFFxxxxWEBPrest"), false},
		{"gif", "image/gif", []byte("GIF89arest"), false},
		{"avif", "image/avif", []byte("xxxxftypavifrest"), false},
		{"svg", "image/svg+xml", []byte("<svg/>"), true},
		{"html", "text/html", []byte("<html>"), true},
		{"json", "application/json", []byte(`{"image":true}`), true},
		{"octet stream", "application/octet-stream", []byte{0xff, 0xd8, 0xff}, true},
		{"spoofed", "image/png", []byte("not png"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Errorf("unexpected upstream credentials: authorization=%t cookie=%t", r.Header.Get("Authorization") != "", r.Header.Get("Cookie") != "")
				}
				w.Header().Set("Content-Type", tc.typ)
				_, _ = w.Write(tc.body)
			})
			defer server.Close()
			body, typ, err := imageTestFetch(t, server, context.Background(), time.Second)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if err == nil && (typ != tc.typ || string(body) != string(tc.body)) {
				t.Fatalf("result type=%s body=%q", typ, body)
			}
		})
	}
}

func TestFetchImageSSRFRedirectAndSanitizedStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ips    []netip.Addr
		status int
	}{
		{"private", []netip.Addr{netip.MustParseAddr("127.0.0.1")}, 200},
		{"mixed", []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")}, 200},
		{"redirect", []netip.Addr{netip.MustParseAddr("93.184.216.34")}, 302},
		{"upstream failure", []netip.Addr{netip.MustParseAddr("93.184.216.34")}, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if tc.status == 302 {
					w.Header().Set("Location", "https://other.example/x?secret=leak")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "private upstream body")
			})
			defer server.Close()
			cert, _ := x509.ParseCertificate(server.Certificate().Raw)
			roots := x509.NewCertPool()
			roots.AddCert(cert)
			var dials atomic.Int32
			resolve := func(context.Context, string) ([]netip.Addr, error) { return tc.ips, nil }
			dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
				dials.Add(1)
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			_, _, err := fetchImageWithTimeout(context.Background(), "https://events.example/picture", resolve, dial, &tls.Config{RootCAs: roots}, time.Second)
			if err == nil || strings.Contains(err.Error(), "leak") || strings.Contains(err.Error(), "upstream body") {
				t.Fatalf("unsafe error: %v", err)
			}
			if (tc.name == "private" || tc.name == "mixed") && dials.Load() != 0 {
				t.Fatalf("dialed unsafe DNS result %d times", dials.Load())
			}
		})
	}
}

func TestFetchImageRejectsHTTPAndOversizeAndTimesOut(t *testing.T) {
	if _, _, err := FetchImage(context.Background(), "http://images.example/picture"); err == nil {
		t.Fatal("HTTP accepted")
	}
	large := newTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
		_, _ = w.Write(make([]byte, ImageMaxResponseBytes))
	})
	defer large.Close()
	if _, _, err := imageTestFetch(t, large, context.Background(), time.Second); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("oversize err=%v", err)
	}
	slow := newTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff})
	})
	defer slow.Close()
	_, _, err := imageTestFetch(t, slow, context.Background(), 20*time.Millisecond)
	if err == nil || strings.Contains(err.Error(), "images.example") {
		t.Fatalf("timeout err=%v", err)
	}
}

func TestFetchImageRejectsTLSHostnameMismatch(t *testing.T) {
	server := newTLSServerWithName(t, "wrong.example", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff})
	})
	defer server.Close()
	_, _, err := imageTestFetch(t, server, context.Background(), time.Second)
	if err == nil || strings.Contains(err.Error(), "wrong.example") {
		t.Fatalf("TLS error=%v", err)
	}
}
