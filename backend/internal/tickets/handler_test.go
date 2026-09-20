package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

type fakeProvider struct {
	url     string
	err     error
	calls   int
	userID  uuid.UUID
	eventID uuid.UUID
}

func (f *fakeProvider) Click(_ context.Context, userID, eventID uuid.UUID) (string, error) {
	f.calls++
	f.userID, f.eventID = userID, eventID
	return f.url, f.err
}

func ticketTestRouter(provider Provider) http.Handler {
	h := NewHandler(provider)
	return httpapi.NewRouter(nil, slog.Default(), func(r chi.Router) {
		h.RegisterRoutes(r, func(next http.Handler) http.Handler { return next })
	})
}

func ticketRequest(userID uuid.UUID, eventID uuid.UUID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+eventID.String()+"/ticket-click", strings.NewReader(body))
	return req.WithContext(contracts.WithPrincipal(req.Context(), contracts.Principal{UserID: userID}))
}

func TestTicketClickRequiresPrincipalAndStrictJSON(t *testing.T) {
	user, eventID := uuid.New(), uuid.New()
	provider := &fakeProvider{url: "https://tickets.example.test/event"}
	h := ticketTestRouter(provider)

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/events/"+eventID.String()+"/ticket-click", strings.NewReader(`{"source":"feed"}`)))
	if res.Code != http.StatusUnauthorized || res.Header().Get("WWW-Authenticate") != "Bearer" || provider.calls != 0 {
		t.Fatalf("unauthenticated status/calls/header = %d/%d/%q", res.Code, provider.calls, res.Header().Get("WWW-Authenticate"))
	}

	for _, body := range []string{
		`{}`, `{"source":"unknown"}`, `{"source":null}`, `{"source":"feed","extra":true}`,
		`{"source":"feed","room_id":"not-a-uuid"}`, `{"source":"feed"} trailing`,
	} {
		t.Run(body, func(t *testing.T) {
			res := httptest.NewRecorder()
			h.ServeHTTP(res, ticketRequest(user, eventID, body))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
	if provider.calls != 0 {
		t.Fatalf("provider called %d times for invalid requests", provider.calls)
	}
}

func TestTicketClickReturnsVerifiedURLAndCanonicalErrors(t *testing.T) {
	user, eventID := uuid.New(), uuid.New()
	provider := &fakeProvider{url: "https://tickets.example.test/event"}
	h := ticketTestRouter(provider)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, ticketRequest(user, eventID, `{"source":"match","room_id":null}`))
	if res.Code != http.StatusOK || provider.calls != 1 || provider.userID != user || provider.eventID != eventID {
		t.Fatalf("success status/provider = %d/%#v; body=%s", res.Code, provider, res.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil || response["external_url"] != provider.url || len(response) != 1 {
		t.Fatalf("response=%s err=%v", res.Body.String(), err)
	}

	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{ErrUnavailable, http.StatusConflict, "TICKET_UNAVAILABLE"},
		{errors.New("database failure"), http.StatusInternalServerError, "INTERNAL"},
	} {
		provider.err = tc.err
		res := httptest.NewRecorder()
		h.ServeHTTP(res, ticketRequest(user, eventID, `{"source":"saved"}`))
		if res.Code != tc.status || !strings.Contains(res.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatalf("error=%v status/body=%d/%s", tc.err, res.Code, res.Body.String())
		}
	}
}

func TestTicketClickInvalidEventDoesNotCallProvider(t *testing.T) {
	provider := &fakeProvider{}
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/not-a-uuid/ticket-click", strings.NewReader(`{"source":"feed"}`))
	req = req.WithContext(contracts.WithPrincipal(req.Context(), contracts.Principal{UserID: uuid.New()}))
	ticketTestRouter(provider).ServeHTTP(res, req)
	if res.Code != http.StatusNotFound || provider.calls != 0 {
		t.Fatalf("status/calls=%d/%d", res.Code, provider.calls)
	}
}
