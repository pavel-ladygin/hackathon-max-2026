-- name: GetActiveMembership :one
SELECT *
FROM room_members
WHERE user_id = $1 AND is_active = true;

-- name: LockMembershipUser :one
-- Lock the user row first. This serializes the no-membership case without a
-- key-share deadlock against room_members' foreign key.
SELECT id
FROM users
WHERE id = $1
FOR NO KEY UPDATE;

-- name: LockActiveMembership :one
-- Call after the user lock and after any affected rooms are locked.
SELECT *
FROM room_members
WHERE user_id = $1 AND is_active = true
FOR UPDATE;

-- name: GetRoomMembership :one
SELECT *
FROM room_members
WHERE room_id = $1 AND user_id = $2;

-- name: CountRoomMembers :one
SELECT count(*)
FROM room_members
WHERE room_id = $1;

-- name: GetPublicParticipants :many
SELECT u.id AS user_id, u.display_name, u.avatar_url, m.role,
       COALESCE(s.ready, false) AS intent_ready
FROM room_members AS m
JOIN users AS u ON u.id = m.user_id
LEFT JOIN room_member_round_state AS s
  ON s.room_id = m.room_id AND s.user_id = m.user_id AND s.round_no = $2
WHERE m.room_id = $1
ORDER BY m.joined_at, m.user_id;

-- name: InsertRoomMember :one
-- The caller locks the room first. The predicate enforces capacity and makes
-- the creator membership agree with rooms.creator_user_id.
INSERT INTO room_members (room_id, user_id, role)
SELECT $1, $2, $3
FROM rooms AS r
WHERE r.id = $1
  AND (SELECT count(*) FROM room_members WHERE room_id = $1) < 2
  AND (
    ($3 = 'creator' AND r.creator_user_id = $2)
    OR ($3 = 'participant' AND EXISTS (
      SELECT 1 FROM room_members WHERE room_id = $1 AND role = 'creator'
    ))
  )
RETURNING *;

-- name: RetireRoomMemberships :execrows
UPDATE room_members
SET is_active = false
WHERE room_id = $1 AND is_active = true;

-- name: GetRoomCloseNotice :one
SELECT n.room_id, r.name AS room_name, r.closed_by, closer.display_name AS closed_by_display_name, r.closed_at
FROM room_close_notices n
JOIN rooms r ON r.id = n.room_id
JOIN users closer ON closer.id = r.closed_by
WHERE n.recipient_user_id = $1 AND n.acknowledged_at IS NULL
ORDER BY n.created_at DESC
LIMIT 1;
