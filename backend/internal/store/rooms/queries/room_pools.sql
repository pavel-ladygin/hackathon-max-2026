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
