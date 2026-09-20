-- +goose Up
CREATE TABLE room_member_round_state (
    room_id uuid NOT NULL, user_id uuid NOT NULL, round_no smallint NOT NULL CHECK (round_no BETWEEN 1 AND 3),
    intent_version integer, ready boolean NOT NULL DEFAULT false, pool_finished boolean NOT NULL DEFAULT false,
    ready_at timestamptz, finished_at timestamptz, PRIMARY KEY(room_id,user_id,round_no),
    FOREIGN KEY(room_id,user_id) REFERENCES room_members(room_id,user_id) ON DELETE CASCADE
);
CREATE TABLE room_intents (
    room_id uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    round_no smallint NOT NULL CHECK (round_no BETWEEN 1 AND 3), date_options date[] NOT NULL,
    day_types text[] NOT NULL DEFAULT '{}', time_slots text[] NOT NULL DEFAULT '{}', category_slugs text[] NOT NULL DEFAULT '{}',
    budget_max_minor integer NOT NULL CHECK (budget_max_minor >= 0), location_lat double precision,
    location_lng double precision, radius_m integer, exclusion_slugs text[] NOT NULL DEFAULT '{}',
    free_text text CHECK (free_text IS NULL OR char_length(free_text) <= 300),
    version integer NOT NULL DEFAULT 1 CHECK (version >= 1), submitted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(room_id,user_id,round_no), CHECK ((location_lat IS NULL) = (location_lng IS NULL)),
    CHECK (radius_m IS NULL OR radius_m BETWEEN 100 AND 50000),
    FOREIGN KEY(room_id,user_id) REFERENCES room_members(room_id,user_id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE room_intents;
DROP TABLE room_member_round_state;
