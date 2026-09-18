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
