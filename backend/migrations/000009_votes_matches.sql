-- +goose Up
CREATE TABLE room_votes (
    pool_id uuid NOT NULL REFERENCES room_pools(id) ON DELETE CASCADE,
    room_id uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    event_id uuid NOT NULL REFERENCES events(id), user_id uuid NOT NULL REFERENCES users(id),
    vote text NOT NULL CHECK(vote IN ('like','dislike')), created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(pool_id,event_id,user_id),
    FOREIGN KEY(pool_id,event_id) REFERENCES room_pool_events(pool_id,event_id),
    FOREIGN KEY(pool_id,room_id) REFERENCES room_pools(id,room_id),
    FOREIGN KEY(room_id,user_id) REFERENCES room_members(room_id,user_id)
);
CREATE TABLE room_matches (
    id uuid PRIMARY KEY, room_id uuid NOT NULL UNIQUE REFERENCES rooms(id),
    pool_id uuid NOT NULL REFERENCES room_pools(id), event_id uuid NOT NULL REFERENCES events(id),
    matched_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(pool_id,event_id) REFERENCES room_pool_events(pool_id,event_id),
    FOREIGN KEY(pool_id,room_id) REFERENCES room_pools(id,room_id)
);

-- +goose Down
DROP TABLE room_matches;
DROP TABLE room_votes;
