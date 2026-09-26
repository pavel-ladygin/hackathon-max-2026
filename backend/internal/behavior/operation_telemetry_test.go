package behavior

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

func TestOperationMiddlewareRecordsBoundedFactsWithoutRequestContents(t *testing.T) {
	userID := uuid.New()
	var events []contracts.ServerBehaviorEvent
	sink := func(_ context.Context, event contracts.ServerBehaviorEvent) { events = append(events, event) }
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: userID})))
		})
	}
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) {
		r.With(authenticate, OperationMiddleware("room_join", sink)).Post("/api/v1/room-invites/{token}/join", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "invalid invite", http.StatusConflict)
		})
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/room-invites/secret-token-value/join?raw_query=private", strings.NewReader("raw-body-secret"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || len(events) != 2 {
		t.Fatalf("status/events=%d/%d, want 409/two", response.Code, len(events))
	}
	if events[0].Type != "api_performance" || events[1].Type != "api_error" || events[0].UserID != userID || events[0].RequestID == "" {
		t.Fatalf("unexpected operational events: %#v", events)
	}
	for _, event := range events {
		if !validServerEvent(event) {
			t.Fatalf("server operational event failed validation: %#v", event)
		}
		var properties map[string]any
		if err := json.Unmarshal(event.Properties, &properties); err != nil {
			t.Fatal(err)
		}
		if properties["operation"] != "room_join" || properties["status_code"] != float64(http.StatusConflict) || properties["critical"] != true || properties["duration_ms"].(float64) < 0 || properties["duration_ms"].(float64) > 600000 {
			t.Fatalf("unexpected bounded properties: %#v", properties)
		}
		if strings.Contains(string(event.Properties), "secret-token-value") || strings.Contains(string(event.Properties), "private") || strings.Contains(string(event.Properties), "raw-body-secret") {
			t.Fatalf("request contents leaked into event: %s", event.Properties)
		}
	}
}

func TestAsynchronousOperationSinkDoesNotWaitForStorage(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	queue := newOperationQueue(func(contracts.ServerBehaviorEvent) {
		close(started)
		<-release
	})
	defer func() {
		releaseOnce.Do(func() { close(release) })
		queue.Close()
		<-queue.done
	}()
	sink := queue.Sink()
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: uuid.New()})))
		})
	}
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) {
		r.With(authenticate, OperationMiddleware("feed_load", sink)).Get("/api/v1/feed/home", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/feed/home", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("response status=%d, want 200", response.Code)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("analytics writer did not start")
	}
	releaseOnce.Do(func() { close(release) })
}

func TestOperationMiddlewareRecordsPanicAsServerError(t *testing.T) {
	var events []contracts.ServerBehaviorEvent
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: uuid.New()})))
		})
	}
	router := httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) {
		r.With(authenticate, OperationMiddleware("feed_load", func(_ context.Context, event contracts.ServerBehaviorEvent) { events = append(events, event) })).Get("/api/v1/feed/home", func(http.ResponseWriter, *http.Request) {
			panic("private panic detail")
		})
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/feed/home", nil))
	if response.Code != http.StatusInternalServerError || len(events) != 2 || events[0].Type != "api_performance" || events[1].Type != "api_error" {
		t.Fatalf("status/events=%d/%#v, want safe 500 and both operational events", response.Code, events)
	}
	if strings.Contains(string(events[0].Properties), "private panic detail") {
		t.Fatalf("panic detail leaked into operational event: %s", events[0].Properties)
	}
}
