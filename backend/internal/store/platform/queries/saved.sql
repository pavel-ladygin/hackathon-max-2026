-- Saved-event projections intentionally omit ticket_url and all private room data.

-- name: SavedEventExists :one
SELECT EXISTS(SELECT 1 FROM events WHERE id = $1);

-- name: InsertSavedEvent :execrows
INSERT INTO saved_events (user_id, event_id)
VALUES ($1, $2)
ON CONFLICT (user_id, event_id) DO NOTHING;

-- name: GetSavedEventCreatedAt :one
SELECT created_at
FROM saved_events
WHERE user_id = $1 AND event_id = $2;

-- name: DeleteSavedEvent :exec
DELETE FROM saved_events WHERE user_id = $1 AND event_id = $2;

-- name: ListSavedEventCards :many
SELECT se.created_at AS saved_at,
       e.id, e.title, e.subtitle,
       (SELECT ec.category_slug FROM event_categories ec WHERE ec.event_id = e.id AND ec.is_primary) AS category_slug,
       e.starts_at, e.timezone, v.name AS venue_name, e.price_from_minor, e.currency,
       coalesce(image.url, '') AS image_url
FROM saved_events se
JOIN events e ON e.id = se.event_id
JOIN venues v ON v.id = e.venue_id
LEFT JOIN LATERAL (
    SELECT ei.url FROM event_images ei WHERE ei.event_id = e.id
    ORDER BY CASE ei.role WHEN 'card' THEN 0 WHEN 'hero' THEN 1 ELSE 2 END, ei.position, ei.id LIMIT 1
) image ON true
WHERE se.user_id = sqlc.arg('user_id')
  AND EXISTS (SELECT 1 FROM event_categories ec WHERE ec.event_id = e.id AND ec.is_primary)
  AND (sqlc.narg('cursor_saved_at')::timestamptz IS NULL
       OR (se.created_at, e.id) < (sqlc.narg('cursor_saved_at')::timestamptz, sqlc.narg('cursor_event_id')::uuid))
ORDER BY se.created_at DESC, e.id DESC
LIMIT sqlc.arg('limit_count');

-- name: ListMatchedEventCards :many
WITH scoped_matches AS (
    SELECT rm.id, rm.room_id, rm.event_id, rm.matched_at
    FROM room_matches rm
    JOIN room_members viewer ON viewer.room_id = rm.room_id
    WHERE viewer.user_id = sqlc.arg('user_id')
      AND (sqlc.narg('cursor_matched_at')::timestamptz IS NULL
           OR (rm.matched_at, rm.id) < (sqlc.narg('cursor_matched_at')::timestamptz, sqlc.narg('cursor_match_id')::uuid))
    ORDER BY rm.matched_at DESC, rm.id DESC
    LIMIT sqlc.arg('limit_count')
)
SELECT sm.id AS match_id, sm.room_id, sm.event_id, sm.matched_at,
       e.title, e.subtitle,
       (SELECT ec.category_slug FROM event_categories ec WHERE ec.event_id = e.id AND ec.is_primary) AS category_slug,
       e.starts_at, e.timezone, v.name AS venue_name, e.price_from_minor, e.currency,
       coalesce(image.url, '') AS image_url,
       EXISTS (SELECT 1 FROM saved_events se WHERE se.user_id = sqlc.arg('user_id') AND se.event_id = e.id) AS saved,
       participant.user_id AS participant_id, participant_user.display_name, participant_user.avatar_url, participant.role,
       coalesce(round_state.ready, false) AS intent_ready
FROM scoped_matches sm
JOIN rooms r ON r.id = sm.room_id
JOIN events e ON e.id = sm.event_id
JOIN venues v ON v.id = e.venue_id
JOIN room_members participant ON participant.room_id = sm.room_id
JOIN users participant_user ON participant_user.id = participant.user_id
LEFT JOIN room_member_round_state round_state
  ON round_state.room_id = participant.room_id AND round_state.user_id = participant.user_id AND round_state.round_no = r.round_no
LEFT JOIN LATERAL (
    SELECT ei.url FROM event_images ei WHERE ei.event_id = e.id
    ORDER BY CASE ei.role WHEN 'card' THEN 0 WHEN 'hero' THEN 1 ELSE 2 END, ei.position, ei.id LIMIT 1
) image ON true
WHERE EXISTS (SELECT 1 FROM event_categories ec WHERE ec.event_id = e.id AND ec.is_primary)
ORDER BY sm.matched_at DESC, sm.id DESC, participant.joined_at, participant.user_id;
