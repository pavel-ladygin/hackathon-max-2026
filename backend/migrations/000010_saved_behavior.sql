-- +goose Up
CREATE TABLE saved_events (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,event_id)
);
CREATE TABLE behavior_events (
    id uuid PRIMARY KEY, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    origin text NOT NULL CHECK(origin IN ('client','server')), type text NOT NULL,
    event_id uuid REFERENCES events(id), room_id uuid REFERENCES rooms(id), surface text, position integer,
    request_id text, client_event_id text, occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    CHECK (origin='server' OR type IN ('impression','open','share')),
    CHECK (origin='server' OR client_event_id IS NOT NULL)
);

-- +goose Down
DROP TABLE behavior_events;
DROP TABLE saved_events;
