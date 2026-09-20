package saved

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

const requestLimit = 1024

type Provider interface {
	Set(context.Context, uuid.UUID, uuid.UUID, bool) (State, error)
	List(context.Context, uuid.UUID, ListInput) (Page, error)
	EncodeCursor(uuid.UUID, Tab, Cursor) (string, error)
	DecodeCursor(uuid.UUID, Tab, string) (Cursor, error)
}
type Handler struct{ service Provider }

func NewHandler(service Provider) *Handler { return &Handler{service: service} }
func (h *Handler) RegisterRoutes(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.With(authenticate).Put("/api/v1/me/saved-events/{eventId}", h.Set)
	r.With(authenticate).Get("/api/v1/me/saved-events", h.List)
}
func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	eventID, err := uuid.Parse(chi.URLParam(r, "eventId"))
	if err != nil || eventID == uuid.Nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Event not found")
		return
	}
	saved, err := savedRequest(w, r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid saved event request")
		return
	}
	result, err := h.service.Set(r.Context(), principal.UserID, eventID, saved)
	if errors.Is(err, ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Event not found")
		return
	}
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid saved event request")
		} else {
			writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		}
		return
	}
	response := api.SavedStateResponse{EventId: result.EventID, Saved: result.Saved, SavedAt: nullable.NewNullNullable[time.Time]()}
	if result.SavedAt != nil {
		response.SavedAt = nullable.NewNullableWithValue(*result.SavedAt)
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	input, err := listInput(r, principal.UserID, h.service)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid saved events request")
		return
	}
	page, err := h.service.List(r.Context(), principal.UserID, input)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid saved events request")
		} else {
			writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		}
		return
	}
	response, err := pageResponse(page, principal.UserID, input.Tab, h.service)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}
func savedRequest(w http.ResponseWriter, r *http.Request) (bool, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestLimit))
	if err != nil || !utf8.Valid(body) {
		return false, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields) != 1 || fields["saved"] == nil || bytes.Equal(fields["saved"], []byte("null")) {
		return false, ErrInvalid
	}
	var saved bool
	if json.Unmarshal(fields["saved"], &saved) != nil {
		return false, ErrInvalid
	}
	return saved, nil
}
func listInput(r *http.Request, userID uuid.UUID, provider Provider) (ListInput, error) {
	query := r.URL.Query()
	tab := TabSaved
	if values, ok := query["tab"]; ok {
		if len(values) != 1 || (values[0] != "saved" && values[0] != "matches") {
			return ListInput{}, ErrInvalid
		}
		tab = Tab(values[0])
	}
	input := ListInput{Tab: tab, Limit: defaultLimit}
	if values, ok := query["limit"]; ok {
		if len(values) != 1 || values[0] == "" {
			return ListInput{}, ErrInvalid
		}
		limit, err := strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > maxLimit {
			return ListInput{}, ErrInvalid
		}
		input.Limit = limit
	}
	if values, ok := query["cursor"]; ok {
		if len(values) != 1 {
			return ListInput{}, ErrInvalid
		}
		cursor, err := provider.DecodeCursor(userID, tab, values[0])
		if err != nil {
			return ListInput{}, err
		}
		input.Cursor = &cursor
	}
	return input, nil
}
func pageResponse(page Page, userID uuid.UUID, tab Tab, provider Provider) (api.SavedEventsResponse, error) {
	response := api.SavedEventsResponse{Items: make([]struct {
		Event   api.EventCard                       `json:"event"`
		Match   nullable.Nullable[api.MatchSummary] `json:"match,omitempty"`
		SavedAt nullable.Nullable[time.Time]        `json:"saved_at,omitempty"`
	}, len(page.Items)), NextCursor: nullable.NewNullNullable[string]()}
	for i, item := range page.Items {
		entry := struct {
			Event   api.EventCard                       `json:"event"`
			Match   nullable.Nullable[api.MatchSummary] `json:"match,omitempty"`
			SavedAt nullable.Nullable[time.Time]        `json:"saved_at,omitempty"`
		}{Event: eventCard(item.Event), Match: nullable.NewNullNullable[api.MatchSummary](), SavedAt: nullable.NewNullNullable[time.Time]()}
		if item.SavedAt != nil {
			entry.SavedAt = nullable.NewNullableWithValue(*item.SavedAt)
		}
		if item.Match != nil {
			entry.Match = nullable.NewNullableWithValue(matchSummary(*item.Match))
		}
		response.Items[i] = entry
	}
	if page.NextCursor != nil {
		value, err := provider.EncodeCursor(userID, tab, *page.NextCursor)
		if err != nil {
			return api.SavedEventsResponse{}, err
		}
		response.NextCursor = nullable.NewNullableWithValue(value)
	}
	return response, nil
}
func eventCard(card Card) api.EventCard {
	result := api.EventCard{Id: card.ID, Title: card.Title, CategorySlug: api.CategorySlug(card.CategorySlug), StartsAt: card.StartsAt, Timezone: card.Timezone, DateLabel: card.DateLabel, VenueName: card.VenueName, Currency: api.EventCardCurrency(card.Currency), PriceLabel: card.PriceLabel, Saved: card.Saved, Reasons: []api.RecommendationReason{}, ImageUrl: nullable.NewNullNullable[string](), PriceFromMinor: nullable.NewNullNullable[int](), DistanceM: nullable.NewNullNullable[int](), DistanceLabel: nullable.NewNullNullable[string](), Subtitle: nullable.NewNullNullable[string]()}
	if card.Subtitle != nil {
		result.Subtitle = nullable.NewNullableWithValue(*card.Subtitle)
	}
	if card.ImageURL != nil {
		result.ImageUrl = nullable.NewNullableWithValue(*card.ImageURL)
	}
	if card.PriceFromMinor != nil {
		result.PriceFromMinor = nullable.NewNullableWithValue(*card.PriceFromMinor)
	}
	return result
}
func matchSummary(match Match) api.MatchSummary {
	result := api.MatchSummary{Id: match.ID, RoomId: match.RoomID, EventId: match.EventID, MatchedAt: match.MatchedAt, Participants: make([]api.PublicParticipant, len(match.Participants))}
	for i, participant := range match.Participants {
		value := api.PublicParticipant{Id: participant.ID, DisplayName: participant.DisplayName, Role: api.PublicParticipantRole(participant.Role), IntentReady: participant.IntentReady, AvatarUrl: nullable.NewNullNullable[string]()}
		if participant.AvatarURL != nil {
			value.AvatarUrl = nullable.NewNullableWithValue(*participant.AvatarURL)
		}
		result.Participants[i] = value
	}
	return result
}
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}
