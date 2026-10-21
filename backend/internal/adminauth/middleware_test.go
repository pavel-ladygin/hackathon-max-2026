package adminauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareProtectsInternalAPIAndSetsVerifiedActor(t *testing.T) {
	middleware := NewMiddleware("shared-test-secret")
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := User(r); got != "fixture-admin" {
			t.Errorf("User() = %q, want fixture-admin", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	protected := middleware(next)

	for _, path := range []string{"/api/v1/internal", "/api/v1/internal/events"} {
		called = false
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Internal-Admin-Secret", "shared-test-secret")
		req.Header.Set("X-Internal-Admin-User", "fixture-admin")
		res := httptest.NewRecorder()
		protected.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent || !called {
			t.Fatalf("%s: status=%d called=%v", path, res.Code, called)
		}
	}
}

func TestMiddlewareFailsClosedForMissingOrInvalidCredentials(t *testing.T) {
	for _, tc := range []struct {
		name      string
		secret    string
		setSecret bool
		user      string
		want      int
	}{
		{name: "unset server secret", user: "admin", want: http.StatusServiceUnavailable},
		{name: "missing request secret", secret: "configured", user: "admin", want: http.StatusForbidden},
		{name: "wrong request secret", secret: "configured", setSecret: true, user: "admin", want: http.StatusForbidden},
		{name: "missing user", secret: "configured", setSecret: true, user: "", want: http.StatusForbidden},
		{name: "control character in user", secret: "configured", setSecret: true, user: "admin\nattacker", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := NewMiddleware(tc.secret)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/events", nil)
			if tc.setSecret {
				secret := "configured"
				if tc.name == "wrong request secret" {
					secret = "wrong"
				}
				req.Header.Set("X-Internal-Admin-Secret", secret)
			}
			if tc.user != "" {
				req.Header.Set("X-Internal-Admin-User", tc.user)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want || called {
				t.Fatalf("status=%d called=%v, want status=%d and no handler call", res.Code, called, tc.want)
			}
		})
	}
}

func TestMiddlewareRejectsDuplicateProxyHeaders(t *testing.T) {
	for _, header := range []string{"X-Internal-Admin-Secret", "X-Internal-Admin-User"} {
		t.Run(header, func(t *testing.T) {
			called := false
			handler := NewMiddleware("shared-test-secret")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/events", nil)
			req.Header.Set("X-Internal-Admin-Secret", "shared-test-secret")
			req.Header.Set("X-Internal-Admin-User", "fixture-admin")
			req.Header.Add(header, req.Header.Get(header))
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusForbidden || called {
				t.Fatalf("status=%d called=%v, want forbidden and no handler call", res.Code, called)
			}
		})
	}
}

func TestMiddlewareLeavesPublicAPIUntouched(t *testing.T) {
	called := false
	handler := NewMiddleware("")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(204) }))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
	if res.Code != 204 || !called {
		t.Fatalf("public request status=%d called=%v", res.Code, called)
	}
}
