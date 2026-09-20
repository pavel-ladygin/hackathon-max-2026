package tickets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

const ticketClickRequestLimit = 4 * 1024

type Provider interface {
	Click(context.Context, uuid.UUID, uuid.UUID) (string, error)
}

type Handler struct{ service Provider }

func NewHandler(service Provider) *Handler { return &Handler{service: service} }

func (h *Handler) RegisterRoutes(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.With(authenticate).Post("/api/v1/events/{eventId}/ticket-click", h.Click)
}

func (h *Handler) Click(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeTicketError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	eventID, err := uuid.Parse(chi.URLParam(r, "eventId"))
	if err != nil || eventID == uuid.Nil {
		writeTicketError(w, r, http.StatusNotFound, "NOT_FOUND", "Event not found")
		return
	}
	if err := decodeTicketClickRequest(w, r); err != nil {
		writeTicketError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid ticket click request")
		return
	}
	externalURL, err := h.service.Click(r.Context(), principal.UserID, eventID)
	if errors.Is(err, ErrNotFound) {
		writeTicketError(w, r, http.StatusNotFound, "NOT_FOUND", "Event not found")
		return
	}
	if errors.Is(err, ErrUnavailable) {
		writeTicketError(w, r, http.StatusConflict, "TICKET_UNAVAILABLE", "Tickets are unavailable")
		return
	}
	if err != nil {
		writeTicketError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, api.TicketClickResponse{ExternalUrl: externalURL})
}

func decodeTicketClickRequest(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, ticketClickRequestLimit))
	if err != nil || !utf8.Valid(body) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || len(fields) < 1 || len(fields) > 2 || fields["source"] == nil || bytes.Equal(fields["source"], []byte("null")) {
		return ErrInvalid
	}
	for name := range fields {
		if name != "source" && name != "room_id" {
			return ErrInvalid
		}
	}
	var source api.TicketClickRequestSource
	if err := json.Unmarshal(fields["source"], &source); err != nil || !source.Valid() {
		return ErrInvalid
	}
	if roomID, ok := fields["room_id"]; ok && !bytes.Equal(roomID, []byte("null")) {
		var value uuid.UUID
		if err := json.Unmarshal(roomID, &value); err != nil || value == uuid.Nil {
			return ErrInvalid
		}
	}
	return nil
}

func writeTicketError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}
