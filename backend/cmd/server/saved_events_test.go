package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestNewHandlerRegistersAuthenticatedSavedEventsRoutes(t *testing.T) {
	db := openServerTestDB(t)
	h, err := newHandler(serverTestConfig(), db, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		method string
		target string
	}{
		{name: "list", method: http.MethodGet, target: "/api/v1/me/saved-events"},
		{name: "set", method: http.MethodPut, target: "/api/v1/me/saved-events/" + uuid.NewString()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.target, bytes.NewBufferString(`{"saved":true}`))
			h.ServeHTTP(res, req)
			if res.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s; want authenticated route response 401", res.Code, res.Body.String())
			}
			if res.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("WWW-Authenticate=%q; want Bearer", res.Header().Get("WWW-Authenticate"))
			}
			if res.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing X-Request-ID")
			}
		})
	}
}
