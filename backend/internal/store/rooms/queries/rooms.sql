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

-- name: CityExists :one
SELECT EXISTS(SELECT 1 FROM cities WHERE id = $1);

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
