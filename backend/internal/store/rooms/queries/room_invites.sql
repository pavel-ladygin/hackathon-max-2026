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

-- name: GetInvitePreviewByHash :one
SELECT i.token_hash, i.expires_at, r.id AS room_id, r.name AS room_name, r.state AS room_state, r.expires_at AS room_expires_at,
       u.id AS inviter_id, u.display_name AS inviter_display_name, u.avatar_url AS inviter_avatar_url,
       COALESCE((SELECT s.ready FROM room_member_round_state s
                 WHERE s.room_id = r.id AND s.user_id = u.id AND s.round_no = r.round_no), false)::boolean AS inviter_intent_ready,
       (SELECT count(*) FROM room_members m WHERE m.room_id = r.id) AS member_count,
       EXISTS (SELECT 1 FROM room_members m WHERE m.room_id = r.id AND m.user_id = $2 AND m.is_active = true) AS already_joined
FROM room_invites i
JOIN rooms r ON r.id = i.room_id
JOIN users u ON u.id = i.created_by
WHERE i.token_hash = $1;

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

-- name: DeleteInviteSecretsExpiredBefore :execrows
-- Removing the row removes both the token hash and ciphertext after the
-- retention period, including for rooms whose memberships were retired earlier.
DELETE FROM room_invites
WHERE expires_at <= $1;
