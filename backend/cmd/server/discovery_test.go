package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHandlerRegistersAuthenticatedDiscoveryRoutes(t *testing.T) {
	db := openServerTestDB(t)
	h, err := newHandler(context.Background(), serverTestConfig(), db, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{
		"/api/v1/feed/home",
		"/api/v1/events/search",
		"/api/v1/events/00000000-0000-0000-0000-000000000001",
	} {
		t.Run(target, func(t *testing.T) {
			res := httptest.NewRecorder()
			h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, target, nil))
			if res.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s; want authenticated route response 401", res.Code, res.Body.String())
			}
		})
	}
}
