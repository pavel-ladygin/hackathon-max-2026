-- name: InsertRoomVote :execrows
INSERT INTO room_votes (pool_id, room_id, event_id, user_id, vote)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (pool_id, event_id, user_id) DO NOTHING;

-- name: GetRoomVote :one
SELECT *
FROM room_votes
WHERE pool_id = $1 AND event_id = $2 AND user_id = $3;

-- name: CountPoolLikes :one
SELECT count(*)
FROM room_votes
WHERE pool_id = $1 AND event_id = $2 AND vote = 'like';

-- name: CountPoolVotesByUser :one
SELECT count(*)
FROM room_votes
WHERE pool_id = $1 AND user_id = $2;
