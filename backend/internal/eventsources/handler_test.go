package eventsources

import (
	"net/http/httptest"
	"testing"
)

func TestRequireMutationChecksAdminHeaderAndSameOrigin(t *testing.T) {
	tests := []struct {
		name, host, origin, adminHeader string
		want                            bool
	}{
		{name: "valid secure origin", host: "admin.example.test", origin: "https://admin.example.test", adminHeader: "1", want: true},
		{name: "missing admin header", host: "admin.example.test", origin: "https://admin.example.test"},
		{name: "missing origin", host: "admin.example.test", adminHeader: "1"},
		{name: "cross origin", host: "admin.example.test", origin: "https://attacker.example.test", adminHeader: "1"},
		{name: "origin path", host: "admin.example.test", origin: "https://admin.example.test/path", adminHeader: "1"},
		{name: "origin userinfo", host: "admin.example.test", origin: "https://user@admin.example.test", adminHeader: "1"},
		{name: "origin fragment", host: "admin.example.test", origin: "https://admin.example.test#fragment", adminHeader: "1"},
		{name: "insecure remote origin", host: "admin.example.test", origin: "http://admin.example.test", adminHeader: "1"},
		{name: "localhost development origin", host: "localhost:8080", origin: "http://localhost:8080", adminHeader: "1", want: true},
		{name: "loopback development origin", host: "127.0.0.1:8080", origin: "http://127.0.0.1:8080", adminHeader: "1", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://"+test.host+"/api/v1/internal/event-sources", nil)
			r.Host = test.host
			if test.origin != "" {
				r.Header.Set("Origin", test.origin)
			}
			if test.adminHeader != "" {
				r.Header.Set("X-Admin-Request", test.adminHeader)
			}
			w := httptest.NewRecorder()
			got := requireMutation(w, r)
			if got != test.want {
				t.Fatalf("requireMutation = %t, want %t; response=%d %s", got, test.want, w.Code, w.Body.String())
			}
			if !test.want && w.Code != 403 {
				t.Fatalf("rejected request status=%d, want 403", w.Code)
			}
		})
	}
}
