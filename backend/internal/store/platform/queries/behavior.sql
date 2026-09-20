-- name: InsertClientBehaviorEvent :execrows
INSERT INTO behavior_events (
    id, user_id, origin, type, event_id, room_id, surface, position,
    request_id, client_event_id, occurred_at
)
VALUES (
    $1, $2, 'client', $3, $4, $5, $6, $7, $8, $9, $10
)
ON CONFLICT (user_id, client_event_id) WHERE client_event_id IS NOT NULL DO NOTHING;
