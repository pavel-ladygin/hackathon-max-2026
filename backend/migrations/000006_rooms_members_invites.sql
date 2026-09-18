-- +goose Up
CREATE TABLE rooms (
    id uuid PRIMARY KEY, creator_user_id uuid NOT NULL REFERENCES users(id), city_id uuid NOT NULL REFERENCES cities(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    state text NOT NULL CHECK (state IN ('collecting_intents','ranking','voting','matched','exhausted')),
    round_no smallint NOT NULL DEFAULT 1 CHECK (round_no BETWEEN 1 AND 3),
    active_pool_version integer NOT NULL DEFAULT 0, matched_event_id uuid REFERENCES events(id),
    version integer NOT NULL DEFAULT 1 CHECK (version >= 1), created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE TABLE room_members (
    room_id uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('creator','participant')), is_active boolean NOT NULL DEFAULT true,
    joined_at timestamptz NOT NULL DEFAULT now(), last_seen_at timestamptz, PRIMARY KEY(room_id,user_id)
);
CREATE TABLE room_invites (
    id uuid PRIMARY KEY, room_id uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE, token_ciphertext bytea NOT NULL, encryption_key_version smallint NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id), expires_at timestamptz NOT NULL,
    consumed_by uuid REFERENCES users(id), consumed_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE room_invites;
DROP TABLE room_members;
DROP TABLE rooms;
