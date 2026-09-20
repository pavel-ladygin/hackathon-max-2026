-- name: InsertRoomPool :one
INSERT INTO room_pools (
  id, room_id, version, round_no, ranker_version, input_fingerprint, state,
  candidate_count, is_small, diagnostics
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: InsertRoomPoolEvents :copyfrom
INSERT INTO room_pool_events (
  pool_id, event_id, position, group_score, participant_score_min,
  participant_score_mean, explanation, feature_snapshot
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetActivePool :one
SELECT p.*
FROM room_pools AS p
JOIN rooms AS r ON r.id = p.room_id AND r.active_pool_version = p.version
WHERE p.room_id = $1;

-- name: LockActivePool :one
SELECT p.*
FROM room_pools AS p
JOIN rooms AS r ON r.id = p.room_id AND r.active_pool_version = p.version
WHERE p.room_id = $1
FOR UPDATE OF p;

-- name: GetRoomPoolEvents :many
SELECT *
FROM room_pool_events
WHERE pool_id = $1
ORDER BY position;

-- name: GetRoomPoolEvent :one
-- The caller must lock the room and active pool before checking membership.
-- The composite primary key makes this a strict pool snapshot membership check.
SELECT *
FROM room_pool_events
WHERE pool_id = $1 AND event_id = $2;

-- name: GetRoomEventCards :many
SELECT pe.event_id, pe.position, pe.explanation, pe.feature_snapshot,
       e.title, e.subtitle, e.starts_at, e.timezone, e.price_from_minor, e.currency,
       v.name AS venue_name,
       (SELECT ec.category_slug FROM event_categories ec
        WHERE ec.event_id = e.id AND ec.is_primary
        ORDER BY ec.category_slug LIMIT 1) AS category_slug,
       coalesce(image.url, '') AS image_url,
       EXISTS (SELECT 1 FROM saved_events se
               WHERE se.user_id = sqlc.arg('user_id') AND se.event_id = e.id) AS saved,
       EXISTS (SELECT 1 FROM room_votes rv
               WHERE rv.pool_id = pe.pool_id AND rv.event_id = pe.event_id
                 AND rv.user_id = sqlc.arg('user_id')) AS voted
FROM room_pool_events pe
JOIN events e ON e.id = pe.event_id
JOIN venues v ON v.id = e.venue_id
LEFT JOIN LATERAL (
    SELECT ei.url FROM event_images ei WHERE ei.event_id = e.id
    ORDER BY CASE ei.role WHEN 'card' THEN 0 WHEN 'hero' THEN 1 ELSE 2 END,
             ei.position, ei.id LIMIT 1
) image ON true
WHERE pe.pool_id = sqlc.arg('pool_id')
  AND EXISTS (SELECT 1 FROM event_categories ec WHERE ec.event_id = e.id AND ec.is_primary)
ORDER BY pe.position;

-- name: GetOldRoomPoolEventIDs :many
SELECT e.event_id
FROM room_pool_events AS e
JOIN room_pools AS p ON p.id = e.pool_id
WHERE p.room_id = $1
ORDER BY p.version, e.position;

-- name: MarkPoolFinished :one
UPDATE room_member_round_state
SET pool_finished = true, finished_at = now()
WHERE room_id = $1 AND user_id = $2 AND round_no = $3
RETURNING *;

-- name: AreBothPoolFinished :one
SELECT count(*) = 2 AND bool_and(s.pool_finished)
FROM room_member_round_state AS s
JOIN room_members AS m ON m.room_id = s.room_id AND m.user_id = s.user_id
WHERE s.room_id = $1 AND s.round_no = $2 AND m.is_active = true;
