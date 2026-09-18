-- name: UpsertMAXUser :one
INSERT INTO users (id, max_user_id, display_name, avatar_url, locale)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (max_user_id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    avatar_url = EXCLUDED.avatar_url,
    locale = EXCLUDED.locale,
    updated_at = now()
RETURNING *;

-- name: CreateAuthSession :exec
INSERT INTO auth_sessions (id, user_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- name: GetAuthSession :one
SELECT user_id, expires_at, revoked_at
FROM auth_sessions WHERE token_hash = $1;
