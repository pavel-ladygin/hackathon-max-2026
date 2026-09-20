-- name: GetUserPreferences :one
SELECT c.id, p.budget_max_minor, p.usual_day_types, p.usual_time_slots,
       p.version, p.updated_at
FROM users AS u
JOIN user_preferences AS p ON p.user_id = u.id
JOIN cities AS c ON c.id = u.city_id
WHERE u.id = $1;

-- name: ListUserPreferenceCategories :many
SELECT category_slug
FROM user_category_preferences
WHERE user_id = $1
ORDER BY category_slug;

-- name: CityExists :one
SELECT EXISTS(SELECT 1 FROM cities WHERE id = $1);

-- name: CompleteUserOnboarding :one
UPDATE users
SET city_id = $2,
    onboarding_state = 'complete',
    updated_at = now()
WHERE id = $1
RETURNING id;

-- name: UpsertUserPreferences :one
INSERT INTO user_preferences (user_id, budget_max_minor, usual_day_types, usual_time_slots)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE
SET budget_max_minor = EXCLUDED.budget_max_minor,
    usual_day_types = EXCLUDED.usual_day_types,
    usual_time_slots = EXCLUDED.usual_time_slots,
    version = user_preferences.version + 1,
    updated_at = now()
RETURNING version, updated_at;

-- name: DeleteUserPreferenceCategories :exec
DELETE FROM user_category_preferences
WHERE user_id = $1;

-- name: AddUserPreferenceCategory :exec
INSERT INTO user_category_preferences (user_id, category_slug, weight, source)
VALUES ($1, $2, 1, 'explicit');
