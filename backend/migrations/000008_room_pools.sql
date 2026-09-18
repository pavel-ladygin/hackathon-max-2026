-- +goose Up
CREATE TABLE room_pools (
    id uuid PRIMARY KEY, room_id uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version >= 1), round_no smallint NOT NULL CHECK (round_no BETWEEN 1 AND 3),
    ranker_version text NOT NULL, input_fingerprint text NOT NULL,
    state text NOT NULL CHECK (state IN ('ranking','ready','exhausted')),
    candidate_count integer NOT NULL CHECK (candidate_count >= 0), is_small boolean NOT NULL DEFAULT false,
    diagnostics jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(room_id,version), UNIQUE(room_id,input_fingerprint), UNIQUE(id,room_id)
);
CREATE TABLE room_pool_events (
    pool_id uuid NOT NULL REFERENCES room_pools(id) ON DELETE CASCADE, event_id uuid NOT NULL REFERENCES events(id),
    position integer NOT NULL CHECK (position >= 0), group_score real NOT NULL,
    participant_score_min real NOT NULL, participant_score_mean real NOT NULL,
    explanation jsonb NOT NULL, feature_snapshot jsonb NOT NULL,
    PRIMARY KEY(pool_id,event_id), UNIQUE(pool_id,position)
);

-- +goose Down
DROP TABLE room_pool_events;
DROP TABLE room_pools;
