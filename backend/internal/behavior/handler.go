package behavior

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
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
		if key != "client_event_id" && key != "type" && key != "occurred_at" && key != "event_id" && key != "room_id" && key != "metadata" && key != "properties" && key != "session_id" && key != "platform" && key != "app_version" && key != "entry_point" && key != "event_version" {
			return ClientEvent{}, ErrInvalid
		}
	}
	if fields["client_event_id"] == nil || fields["type"] == nil || fields["occurred_at"] == nil {
		return ClientEvent{}, ErrInvalid
	}
	var event ClientEvent
	event.EventVersion = 1
	if fields["event_version"] != nil {
		if json.Unmarshal(fields["event_version"], &event.EventVersion) != nil || event.EventVersion != 1 {
			return ClientEvent{}, ErrInvalid
		}
	}
	if json.Unmarshal(fields["client_event_id"], &event.ClientEventID) != nil || len(event.ClientEventID) < 1 || len(event.ClientEventID) > 128 {
		return ClientEvent{}, ErrInvalid
	}
	if json.Unmarshal(fields["type"], &event.Type) != nil || !validClientEventType(event.Type) {
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
		if fields["properties"] != nil {
			return ClientEvent{}, ErrInvalid
		}
		if err := decodeMetadata(fields["metadata"], event.Type, &event); err != nil {
			return ClientEvent{}, err
		}
	}
	if fields["properties"] != nil {
		if err := decodeProperties(fields["properties"], event.Type, &event); err != nil {
			return ClientEvent{}, err
		}
	}
	if event.SessionID, err = nullableUUID(fields["session_id"]); err != nil {
		return ClientEvent{}, err
	}
	if event.Platform, err = optionalString(fields["platform"], 32, map[string]bool{"max_ios": true, "max_android": true, "max_web": true, "max_desktop": true, "browser": true, "unknown": true}); err != nil {
		return ClientEvent{}, err
	}
	if event.AppVersion, err = optionalString(fields["app_version"], 64, nil); err != nil {
		return ClientEvent{}, err
	}
	if event.EntryPoint, err = optionalString(fields["entry_point"], 32, map[string]bool{"feed": true, "home": true, "search": true, "map": true, "saved": true, "room_invite": true, "room": true, "deep_link": true, "unknown": true, "bot": true, "direct": true, "shared_event": true, "recommendation": true}); err != nil {
		return ClientEvent{}, err
	}
	return event, nil
}

func validClientEventType(value string) bool {
	switch value {
	case "impression", "open", "share", "app_opened", "session_started", "onboarding_started", "onboarding_completed", "feed_opened", "event_impression", "event_opened", "search_performed", "filters_opened", "filters_applied", "filters_reset", "map_opened", "map_marker_opened", "room_creation_started", "room_creation_failed", "room_opened", "invite_opened", "invite_shared", "invite_share_failed", "invite_link_opened", "room_join_started", "room_join_failed", "swipe_session_started", "event_swipe_impression", "match_shown", "match_opened", "swipe_pool_exhausted", "ticket_redirect_failed", "client_error", "client_performance":
		return true
	default:
		return false
	}
}

func optionalString(raw json.RawMessage, max int, allowed map[string]bool) (pgtype.Text, error) {
	if raw == nil || bytes.Equal(raw, []byte("null")) {
		return pgtype.Text{}, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || len(value) == 0 || len(value) > max || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') || allowed != nil && !allowed[value] {
		return pgtype.Text{}, ErrInvalid
	}
	return pgtype.Text{String: value, Valid: true}, nil
}

var allowedPropertyKeys = map[string]bool{
	"position": true, "list_type": true, "event_source": true, "category": true, "has_image": true, "has_price": true, "has_coordinates": true, "source_screen": true, "recommendation_rank": true, "rank": true, "recommendation_score": true, "score": true, "algorithm_version": true, "price_min": true, "price_max": true, "distance_km": true, "query_length": true, "result_count": true, "categories": true, "budget_min": true, "budget_max": true, "date_from": true, "date_to": true, "distance_limit_km": true, "previous_active_filter_count": true, "active_filter_count": true, "free_only": true, "zoom_level": true, "pool_size": true, "candidate_count": true, "round_no": true, "participants_count_after_join": true, "participants_count": true, "error_code": true, "operation": true, "duration_ms": true, "status_code": true, "critical": true, "share_method": true, "invite_status": true, "room_state": true, "step": true, "selected_categories_count": true, "budget_configured": true,
}

func decodeProperties(raw json.RawMessage, eventType string, event *ClientEvent) error {
	if len(raw) > 8*1024 {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	for key, value := range fields {
		if !allowedPropertyKeys[key] || value == nil {
			return ErrInvalid
		}
		if bytes.Equal(value, []byte("null")) {
			continue
		}
		if key == "category" || key == "event_source" || key == "list_type" || key == "source_screen" || key == "algorithm_version" || key == "date_from" || key == "date_to" || key == "error_code" || key == "operation" || key == "share_method" || key == "invite_status" || key == "room_state" || key == "step" {
			var text string
			if json.Unmarshal(value, &text) != nil || len(text) > 64 || strings.ContainsRune(text, '\x00') {
				return ErrInvalid
			}
			if key == "operation" && !allowedClientOperation(text) || key == "error_code" && !allowedClientErrorCode(text) {
				return ErrInvalid
			}
			if key == "source_screen" && !oneOf(text, "feed", "direct", "search", "map", "saved", "room", "match", "unknown") || key == "list_type" && !oneOf(text, "feed", "search", "saved", "recommendation", "room") || key == "share_method" && !oneOf(text, "max_share", "native", "clipboard", "unknown") || key == "invite_status" && !oneOf(text, "joinable", "full", "expired") || key == "room_state" && !oneOf(text, "collecting_intents", "ranking", "voting", "matched", "exhausted") || key == "step" && !oneOf(text, "interests", "preferences") {
				return ErrInvalid
			}
			if key == "event_source" && !oneOf(text, "kudago", "timepad", "demo", "unknown") {
				return ErrInvalid
			}
			if key == "category" && !safeCategorySlug(text) {
				return ErrInvalid
			}
			if key == "date_from" || key == "date_to" {
				if _, err := time.Parse(time.DateOnly, text); err != nil {
					return ErrInvalid
				}
			}
		} else if key == "categories" {
			var values []string
			if json.Unmarshal(value, &values) != nil || len(values) > 20 {
				return ErrInvalid
			}
			for _, item := range values {
				if !safeCategorySlug(item) {
					return ErrInvalid
				}
			}
		} else if key == "has_image" || key == "has_price" || key == "has_coordinates" || key == "free_only" || key == "budget_configured" || key == "critical" {
			var boolean bool
			if json.Unmarshal(value, &boolean) != nil {
				return ErrInvalid
			}
		} else {
			var number float64
			if json.Unmarshal(value, &number) != nil || number < 0 || number > 1_000_000_000 {
				return ErrInvalid
			}
			if key == "duration_ms" && (number > 600000 || number != float64(int64(number))) {
				return ErrInvalid
			}
			if key == "status_code" && (number < 100 || number > 599 || number != float64(int(number))) {
				return ErrInvalid
			}
		}
	}
	event.Properties = append([]byte(nil), raw...)
	return nil
}

func safeCategorySlug(value string) bool {
	if len(value) == 0 || len(value) > 40 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func allowedClientOperation(value string) bool {
	switch value {
	case "app_boot", "feed_load", "event_detail_load", "room_create", "room_join", "room_vote":
		return true
	default:
		return false
	}
}

func allowedClientErrorCode(value string) bool {
	switch value {
	case "network_error", "unauthorized", "not_found", "rate_limited", "server_error", "request_error", "unknown":
		return true
	default:
		return false
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
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
