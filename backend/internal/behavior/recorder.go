package behavior

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// Recorder writes server events without allowing an analytics insert failure to
// abort the product transaction. A savepoint is required because PostgreSQL
// marks the whole transaction failed after any statement error.
type Recorder struct{}

var _ contracts.BehaviorRecorder = Recorder{}

func (Recorder) Record(ctx context.Context, db store.DBTX, event contracts.ServerBehaviorEvent) error {
	if !validServerEvent(event) {
		slog.Default().Error("behavior event rejected", "event_type", event.Type, "request_id", event.RequestID)
		return nil
	}
	if _, err := db.Exec(ctx, "SAVEPOINT behavior_event_write"); err != nil {
		slog.Default().Error("behavior savepoint failed", "event_type", event.Type, "request_id", event.RequestID, "error", err)
		return nil
	}
	properties := event.Properties
	if len(properties) == 0 {
		properties = json.RawMessage(`{}`)
	}
	_, writeErr := db.Exec(ctx, `INSERT INTO behavior_events
		(id, user_id, origin, type, event_id, room_id, request_id, occurred_at,
		 event_version, properties, deduplication_key)
		VALUES ($1, $2, 'server', $3, $4, $5, $6, $7, 1, $8::jsonb, $9)
		ON CONFLICT (user_id, deduplication_key) WHERE deduplication_key IS NOT NULL DO NOTHING`,
		event.ID, event.UserID, event.Type, event.EventID, event.RoomID,
		event.RequestID, event.OccurredAt, string(properties), nullableDeduplicationKey(event.DeduplicationKey))
	if writeErr != nil {
		if _, rollbackErr := db.Exec(ctx, "ROLLBACK TO SAVEPOINT behavior_event_write"); rollbackErr != nil {
			slog.Default().Error("behavior write rollback failed", "event_type", event.Type, "request_id", event.RequestID, "error", rollbackErr)
		}
		slog.Default().Error("behavior event write failed", "event_type", event.Type, "request_id", event.RequestID, "error", writeErr)
	}
	if _, err := db.Exec(ctx, "RELEASE SAVEPOINT behavior_event_write"); err != nil {
		slog.Default().Error("behavior savepoint release failed", "event_type", event.Type, "request_id", event.RequestID, "error", err)
	}
	return nil
}

func nullableDeduplicationKey(key string) *string {
	if key == "" {
		return nil
	}
	return &key
}

func validServerEvent(event contracts.ServerBehaviorEvent) bool {
	if event.ID == uuid.Nil || event.UserID == uuid.Nil || event.OccurredAt.IsZero() ||
		len(event.RequestID) > 128 || strings.TrimSpace(event.Type) != event.Type || len(event.DeduplicationKey) > 256 {
		return false
	}
	if len(event.Properties) > 16*1024 || len(event.Properties) > 0 && !json.Valid(event.Properties) {
		return false
	}
	hasEvent := event.EventID != nil && *event.EventID != uuid.Nil
	hasRoom := event.RoomID != nil && *event.RoomID != uuid.Nil
	switch event.Type {
	case "room_created", "room_create", "room_joined", "room_join", "room_pool_generated", "room_intent_submitted", "intent_submit":
		return hasRoom
	case "event_liked", "event_disliked", "like", "dislike", "match_created", "match":
		return hasEvent && hasRoom
	case "save", "unsave", "event_saved", "event_unsaved", "ticket_click":
		return hasEvent
	case "api_performance", "api_error":
		return validOperationProperties(event.Type, event.Properties)
	default:
		return false
	}
}

func validOperationProperties(eventType string, raw json.RawMessage) bool {
	if !json.Valid(raw) {
		return false
	}
	var properties struct {
		Operation  string `json:"operation"`
		StatusCode int    `json:"status_code"`
		DurationMS int64  `json:"duration_ms"`
		Critical   bool   `json:"critical"`
	}
	if json.Unmarshal(raw, &properties) != nil || !allowedServerOperation(properties.Operation) || properties.StatusCode < 100 || properties.StatusCode > 599 || properties.DurationMS < 0 || properties.DurationMS > 600000 || !properties.Critical || eventType == "api_error" && properties.StatusCode < http.StatusBadRequest {
		return false
	}
	return true
}
