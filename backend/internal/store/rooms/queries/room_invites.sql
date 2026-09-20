-- name: InsertRoomInvite :one
INSERT INTO room_invites (
  id, room_id, token_hash, token_ciphertext, encryption_key_version, created_by, expires_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ExpireRoomInvites :execrows
UPDATE room_invites
SET expires_at = LEAST(expires_at, $2)
WHERE room_id = $1;
