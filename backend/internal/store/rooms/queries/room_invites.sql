-- name: InsertRoomInvite :one
INSERT INTO room_invites (
  id, room_id, token_hash, token_ciphertext, encryption_key_version, created_by, expires_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetRoomInviteByHash :one
SELECT *
FROM room_invites
WHERE token_hash = $1;

-- name: GetRoomInviteForCreator :one
SELECT *
FROM room_invites
WHERE room_id = $1 AND created_by = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: LockRoomInviteByHash :one
SELECT *
FROM room_invites
WHERE token_hash = $1
FOR UPDATE;

-- name: MarkRoomInviteConsumed :execrows
UPDATE room_invites
SET consumed_by = COALESCE(consumed_by, $2),
    consumed_at = COALESCE(consumed_at, $3)
WHERE id = $1;

-- name: ExpireRoomInvites :execrows
UPDATE room_invites
SET expires_at = LEAST(expires_at, $2)
WHERE room_id = $1;
