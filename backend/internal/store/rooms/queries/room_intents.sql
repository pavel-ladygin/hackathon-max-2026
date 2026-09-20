-- name: UpsertRoomIntent :one
INSERT INTO room_intents (
  room_id, user_id, round_no, date_options, day_types, time_slots,
  category_slugs, budget_max_minor, location_lat, location_lng, radius_m,
  exclusion_slugs, free_text
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (room_id, user_id, round_no) DO UPDATE
SET date_options = EXCLUDED.date_options,
    day_types = EXCLUDED.day_types,
    time_slots = EXCLUDED.time_slots,
    category_slugs = EXCLUDED.category_slugs,
    budget_max_minor = EXCLUDED.budget_max_minor,
    location_lat = EXCLUDED.location_lat,
    location_lng = EXCLUDED.location_lng,
    radius_m = EXCLUDED.radius_m,
    exclusion_slugs = EXCLUDED.exclusion_slugs,
    free_text = EXCLUDED.free_text,
    version = room_intents.version + 1,
    submitted_at = now()
RETURNING *;

-- name: GetRoomIntent :one
SELECT *
FROM room_intents
WHERE room_id = $1 AND user_id = $2 AND round_no = $3;

-- name: InsertRoomRoundState :one
INSERT INTO room_member_round_state (room_id, user_id, round_no)
VALUES ($1, $2, $3)
RETURNING *;

-- name: SetRoundReady :one
UPDATE room_member_round_state
SET ready = $4,
    intent_version = $5,
    ready_at = CASE WHEN $4 THEN now() ELSE NULL END
WHERE room_id = $1 AND user_id = $2 AND round_no = $3
RETURNING *;

-- name: GetRoomRoundStates :many
SELECT *
FROM room_member_round_state
WHERE room_id = $1 AND round_no = $2
ORDER BY user_id;

-- name: AreBothReady :one
SELECT count(*) = 2 AND bool_and(s.ready)
FROM room_member_round_state AS s
JOIN room_members AS m ON m.room_id = s.room_id AND m.user_id = s.user_id
WHERE s.room_id = $1 AND s.round_no = $2 AND m.is_active = true;

-- name: ResetRoundPoolFinished :execrows
UPDATE room_member_round_state
SET pool_finished = false, finished_at = NULL
WHERE room_id = $1 AND round_no = $2;

-- name: ClearRoomIntentCoordinates :execrows
UPDATE room_intents
SET location_lat = NULL, location_lng = NULL
WHERE room_id = $1 AND location_lat IS NOT NULL;
