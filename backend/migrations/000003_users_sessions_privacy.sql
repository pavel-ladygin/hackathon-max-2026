-- +goose Up
CREATE TABLE users (
    id uuid PRIMARY KEY, max_user_id bigint NOT NULL UNIQUE, display_name text NOT NULL,
    avatar_url text, city_id uuid REFERENCES cities(id), locale text NOT NULL DEFAULT 'ru-RU',
    onboarding_state text NOT NULL DEFAULT 'new' CHECK (onboarding_state IN ('new','complete')),
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE privacy_consents (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, purpose text NOT NULL,
    version text NOT NULL, granted boolean NOT NULL, recorded_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz, PRIMARY KEY (user_id, purpose, version)
);
CREATE TABLE auth_sessions (
    id uuid PRIMARY KEY, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE, expires_at timestamptz NOT NULL, revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE privacy_requests (
    id uuid PRIMARY KEY, user_id uuid NOT NULL REFERENCES users(id),
    type text NOT NULL CHECK(type IN ('access','correction','deletion','restriction')),
    state text NOT NULL, requested_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz,
    audit_note text
);

-- +goose Down
DROP TABLE privacy_requests;
DROP TABLE auth_sessions;
DROP TABLE privacy_consents;
DROP TABLE users;
