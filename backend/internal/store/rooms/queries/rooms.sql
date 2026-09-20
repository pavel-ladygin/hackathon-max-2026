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

-- name: MarkRoomMatched :execrows
-- The caller must have locked the room and verified the active pool/mutual like.
-- The state predicate keeps this transition terminal and idempotent under retries.
UPDATE rooms
SET state = 'matched', matched_event_id = $2, version = version + 1
WHERE id = $1 AND state = 'voting' AND matched_event_id IS NULL;

-- name: MarkRoomExhausted :execrows
-- Exhaustion is only valid from voting; callers must verify both users finished.
UPDATE rooms
SET state = 'exhausted', version = version + 1
WHERE id = $1 AND state = 'voting';

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
