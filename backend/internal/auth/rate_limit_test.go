package auth

import (
	"net"
	"net/http/httptest"
	"testing"
)

func mustCIDR(t *testing.T, value string) net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		t.Fatal(err)
	}
	return *network
}

func TestClientIPUsesForwardedForOnlyForTrustedPeer(t *testing.T) {
	s := &Service{trustedProxyCIDRs: []net.IPNet{mustCIDR(t, "127.0.0.0/8")}}

	tests := []struct {
		name      string
		peer      string
		forwarded string
		want      string
	}{
		{name: "direct peer ignores header", peer: "192.0.2.10:4000", forwarded: "198.51.100.10", want: "192.0.2.10"},
		{name: "trusted proxy uses rightmost untrusted hop", peer: "127.0.0.1:8080", forwarded: "198.51.100.10, 10.0.0.2", want: "10.0.0.2"},
		{name: "trusted chain skips trusted hops", peer: "127.0.0.1:8080", forwarded: "198.51.100.10, 127.0.0.2", want: "198.51.100.10"},
		{name: "invalid entries are ignored", peer: "127.0.0.1:8080", forwarded: "not-an-ip, 198.51.100.10", want: "198.51.100.10"},
		{name: "no header falls back to proxy", peer: "127.0.0.1:8080", want: "127.0.0.1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = test.peer
			if test.forwarded != "" {
				req.Header.Set("X-Forwarded-For", test.forwarded)
			}
			if got := s.clientIP(req); got != test.want {
				t.Fatalf("clientIP() = %q, want %q", got, test.want)
			}
		})
	}
}
