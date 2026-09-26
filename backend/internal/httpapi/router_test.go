package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type readinessFunc func(context.Context) error

func (f readinessFunc) Ready(ctx context.Context) error { return f(ctx) }

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

func TestReadyHealthSuccessHasStableBodyAndRequestID(t *testing.T) {
	r := NewRouter(readinessFunc(func(context.Context) error { return nil }), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/ready", nil)
	req.Header.Set(requestIDHeader, "evil-request-id")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if got := res.Header().Get(requestIDHeader); got == "" || got == "evil-request-id" {
		t.Fatalf("request id header = %q, want generated id", got)
	}
	if got, want := res.Body.String(), "{\"database\":\"ready\",\"migrations\":\"current\",\"status\":\"ready\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReadyHealthFailureUsesCanonicalSafeEnvelope(t *testing.T) {
	r := NewRouter(readinessFunc(func(context.Context) error { return context.Canceled }), slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/ready", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.Code)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "INTERNAL" || body.Error.Message != "service unavailable" || body.Error.RequestID == "" {
		t.Fatalf("error envelope = %#v, want INTERNAL/service unavailable/request id", body.Error)
	}
}

func TestRecoveryReturnsSafeInternalEnvelopeAndLogsCorrelationID(t *testing.T) {
	var logs bytes.Buffer
	h := recovery(testLogger(&logs))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("secret-panic") }))
	req := httptest.NewRequest(http.MethodGet, "/panic", nil).WithContext(context.WithValue(context.Background(), requestIDKey{}, "req-safe"))
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), `"code":"INTERNAL"`) {
		t.Fatalf("response = %d %q, want safe 500 envelope", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-panic") || strings.Contains(logs.String(), "secret-panic") || !strings.Contains(logs.String(), "req-safe") || !strings.Contains(logs.String(), "http handler panic") {
		t.Fatalf("response/logs leaked or missed panic correlation: body=%q logs=%q", res.Body.String(), logs.String())
	}
}

func TestOperationalTelemetryUsesBoundedRouteLabels(t *testing.T) {
	var logs bytes.Buffer
	r := NewRouter(nil, testLogger(&logs), func(r chi.Router) {
		r.Post("/api/v1/room-invites/{token}/join", func(w http.ResponseWriter, _ *http.Request) {
			WriteJSON(w, http.StatusConflict, map[string]string{"code": "ROOM_FULL"})
		})
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/room-invites/secret-token-value/join?secret=query-value", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(logs.String(), `"operation":"room_join"`) || !strings.Contains(logs.String(), `"status":409`) {
		t.Fatalf("status/logs=%d/%s, want room_join conflict telemetry", response.Code, logs.String())
	}
	if strings.Contains(logs.String(), "secret-token-value") || strings.Contains(logs.String(), "query-value") {
		t.Fatalf("operational telemetry leaked request content: %s", logs.String())
	}
}
