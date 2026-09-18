-- name: InsertRoomMatch :execrows
INSERT INTO room_matches (id, room_id, pool_id, event_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (room_id) DO NOTHING;

-- name: GetRoomMatch :one
SELECT *
FROM room_matches
WHERE room_id = $1;
