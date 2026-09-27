-- name: InsertRoom :one
INSERT INTO rooms (id, creator_user_id, city_id, name, state, round_no, active_pool_version, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: LockRoom :one
SELECT *
FROM rooms
WHERE id = $1
FOR UPDATE;

-- name: GetRoom :one
SELECT *
FROM rooms
WHERE id = $1;

-- name: UpdateRoomState :execrows
-- Callers must lock the room and enforce lifecycle preconditions themselves.
UPDATE rooms
SET state = $2, version = version + 1
WHERE id = $1;

-- name: CloseRoom :execrows
UPDATE rooms
SET state = 'closed', closed_by = $2, closed_at = clock_timestamp(), version = version + 1
WHERE id = $1 AND state <> 'closed';

-- name: GetRoomClosureActor :one
SELECT r.closed_by, r.closed_at, u.display_name
FROM rooms r JOIN users u ON u.id = r.closed_by
WHERE r.id = $1 AND r.state = 'closed';

-- name: InsertRoomCloseNotice :exec
INSERT INTO room_close_notices (room_id, recipient_user_id)
VALUES ($1, $2)
ON CONFLICT (room_id, recipient_user_id) DO NOTHING;

-- name: AcknowledgeRoomCloseNotice :execrows
UPDATE room_close_notices
SET acknowledged_at = COALESCE(acknowledged_at, clock_timestamp())
WHERE room_id = $1 AND recipient_user_id = $2;

-- name: MarkRoomMatched :execrows
-- The caller must have locked the room and verified the active pool/mutual like.
-- The state predicate keeps this transition terminal and idempotent under retries.
UPDATE rooms
SET state = 'matched', matched_event_id = sqlc.arg('event_id'), version = version + 1
WHERE id = $1 AND state = 'voting' AND matched_event_id IS NULL;

-- name: MarkRoomExhausted :one
-- Exhaustion is only valid from voting; callers must verify both users finished.
-- The active pool and room transition are one statement, so a committed
-- exhausted room never points at a ready active pool.
WITH exhausted_room AS (
  UPDATE rooms
  SET state = 'exhausted', version = rooms.version + 1
  WHERE rooms.id = $1 AND rooms.state = 'voting'
  RETURNING rooms.id, rooms.active_pool_version, rooms.round_no
), exhausted_pool AS (
  UPDATE room_pools AS p
  SET state = 'exhausted'
  FROM exhausted_room AS r
  WHERE p.room_id = r.id
    AND p.version = r.active_pool_version
    AND p.round_no = r.round_no
    AND p.state = 'ready'
)
SELECT count(*) FROM exhausted_room;

-- name: CityExists :one
SELECT EXISTS(SELECT 1 FROM cities WHERE id = $1);

-- name: GetCityTimezone :one
SELECT timezone
FROM cities
WHERE id = $1;

-- name: TransitionCollectingRoomToRanking :execrows
UPDATE rooms
SET state = 'ranking', version = version + 1
WHERE id = $1 AND state = 'collecting_intents';

-- name: ActivateRoomPool :execrows
UPDATE rooms
SET active_pool_version = $2, state = $3, version = version + 1
WHERE id = $1;

-- name: RestartExhaustedRoomRound :one
-- The caller holds the room row lock.  Keep active_pool_version as history;
-- all active-pool reads are scoped to the new round.
UPDATE rooms
SET state = 'collecting_intents', round_no = round_no + 1, version = version + 1
WHERE id = $1 AND state = 'exhausted' AND round_no < 3
RETURNING *;


-- name: ClockNow :one
SELECT clock_timestamp()::timestamptz;

-- name: GetCreateIdempotency :one
SELECT request_hash, response_status, response_body, expires_at
FROM idempotency_records
WHERE user_id = $1 AND key = $2 AND route = $3;

-- name: DeleteExpiredCreateIdempotency :execrows
DELETE FROM idempotency_records
WHERE user_id = $1 AND key = $2 AND route = $3 AND expires_at <= $4;

-- name: InsertCreateIdempotency :exec
INSERT INTO idempotency_records (
  user_id, key, route, request_hash, response_status, response_body, expires_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ExpireRoomForReplacement :execrows
UPDATE rooms
SET expires_at = LEAST(expires_at, $2), version = version + 1
WHERE id = $1;

-- name: BumpRoomVersion :execrows
UPDATE rooms
SET version = version + 1
WHERE id = $1;

-- name: LockExpiredRoomsForCleanup :many
SELECT *
FROM rooms
WHERE expires_at <= $1
  AND EXISTS (SELECT 1 FROM room_members m WHERE m.room_id = rooms.id AND m.is_active = true)
ORDER BY expires_at, id
LIMIT $2
FOR UPDATE SKIP LOCKED;
