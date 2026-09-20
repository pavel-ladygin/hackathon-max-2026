package behavior

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

const batchRequestLimit = 128 * 1024

type Ingester interface {
	Ingest(context.Context, uuid.UUID, []ClientEvent) (Result, error)
}
type Handler struct{ service Ingester }

func NewHandler(service Ingester) *Handler { return &Handler{service: service} }
func (h *Handler) RegisterRoutes(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.With(authenticate).Post("/api/v1/behavior/events:batch", h.Ingest)
}
func (h *Handler) Ingest(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	events, err := decodeBatch(w, r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid behavior events request")
		return
	}
	result, err := h.service.Ingest(r.Context(), principal.UserID, events)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid behavior events request")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, api.BehaviorBatchResponse{Accepted: result.Accepted, Duplicates: result.Duplicates, Rejected: result.Rejected})
}
func decodeBatch(w http.ResponseWriter, r *http.Request) ([]ClientEvent, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, batchRequestLimit))
	if err != nil || !utf8.Valid(body) {
		return nil, ErrInvalid
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || len(root) != 1 || root["events"] == nil {
		return nil, ErrInvalid
	}
	var rawEvents []json.RawMessage
	if json.Unmarshal(root["events"], &rawEvents) != nil || len(rawEvents) < 1 || len(rawEvents) > 100 {
		return nil, ErrInvalid
	}
	events := make([]ClientEvent, 0, len(rawEvents))
	for _, raw := range rawEvents {
		event, err := decodeEvent(raw)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}
func decodeEvent(raw json.RawMessage) (ClientEvent, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ClientEvent{}, ErrInvalid
	}
	for key := range fields {
		if key != "client_event_id" && key != "type" && key != "occurred_at" && key != "event_id" && key != "room_id" && key != "metadata" {
			return ClientEvent{}, ErrInvalid
		}
	}
	if fields["client_event_id"] == nil || fields["type"] == nil || fields["occurred_at"] == nil {
		return ClientEvent{}, ErrInvalid
	}
	var event ClientEvent
	if json.Unmarshal(fields["client_event_id"], &event.ClientEventID) != nil || len(event.ClientEventID) < 1 || len(event.ClientEventID) > 128 {
		return ClientEvent{}, ErrInvalid
	}
	if json.Unmarshal(fields["type"], &event.Type) != nil || (event.Type != "impression" && event.Type != "open" && event.Type != "share") {
		return ClientEvent{}, ErrInvalid
	}
	var occurred time.Time
	if json.Unmarshal(fields["occurred_at"], &occurred) != nil || occurred.IsZero() {
		return ClientEvent{}, ErrInvalid
	}
	event.OccurredAt = pgtype.Timestamptz{Time: occurred, Valid: true}
	var err error
	if event.EventID, err = nullableUUID(fields["event_id"]); err != nil {
		return ClientEvent{}, err
	}
	if event.RoomID, err = nullableUUID(fields["room_id"]); err != nil {
		return ClientEvent{}, err
	}
	if fields["metadata"] != nil {
		if err := decodeMetadata(fields["metadata"], event.Type, &event); err != nil {
			return ClientEvent{}, err
		}
	}
	return event, nil
}
func nullableUUID(raw json.RawMessage) (pgtype.UUID, error) {
	if raw == nil || bytes.Equal(raw, []byte("null")) {
		return pgtype.UUID{}, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return pgtype.UUID{}, ErrInvalid
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return pgtype.UUID{}, ErrInvalid
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}
func decodeMetadata(raw json.RawMessage, eventType string, event *ClientEvent) error {
	if bytes.Equal(raw, []byte("null")) {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	for key, value := range fields {
		allowed := key == "surface" || key == "request_id" || (key == "position" && eventType == "impression")
		if !allowed || (eventType == "share" && key != "surface") || value == nil || bytes.Equal(value, []byte("null")) {
			return ErrInvalid
		}
	}
	if rawSurface := fields["surface"]; rawSurface != nil {
		var value string
		if json.Unmarshal(rawSurface, &value) != nil || len(value) > 32 {
			return ErrInvalid
		}
		event.Surface = pgtype.Text{String: value, Valid: true}
	}
	if rawRequestID := fields["request_id"]; rawRequestID != nil {
		var value string
		if json.Unmarshal(rawRequestID, &value) != nil || len(value) > 128 {
			return ErrInvalid
		}
		event.RequestID = pgtype.Text{String: value, Valid: true}
	}
	if rawPosition := fields["position"]; rawPosition != nil {
		var value int
		if json.Unmarshal(rawPosition, &value) != nil || value < 0 || value > 2147483647 {
			return ErrInvalid
		}
		event.Position = pgtype.Int4{Int32: int32(value), Valid: true}
	}
	return nil
}
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}
