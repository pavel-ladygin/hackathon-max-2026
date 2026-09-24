-- +goose Up
CREATE TABLE timepad_poster_recovery (
    event_id uuid PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    outcome text NOT NULL CHECK (outcome IN ('error','no_redirect','no_poster','inserted','already_filled')),
    last_error text,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX event_images_event_id_idx ON event_images (event_id);

CREATE INDEX timepad_poster_recovery_due_idx
    ON timepad_poster_recovery (next_attempt_at, event_id)
    WHERE outcome IN ('error','no_redirect','no_poster');

-- +goose Down
DROP INDEX timepad_poster_recovery_due_idx;
DROP INDEX event_images_event_id_idx;
DROP TABLE timepad_poster_recovery;
