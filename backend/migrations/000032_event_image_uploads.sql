-- +goose Up
CREATE TABLE event_image_uploads (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    object_key text NOT NULL UNIQUE,
    content_type text NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
    byte_size bigint NOT NULL CHECK (byte_size BETWEEN 1 AND 10485760),
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    submission_id uuid UNIQUE REFERENCES event_submissions(id) ON DELETE SET NULL,
    deleting boolean NOT NULL DEFAULT false,
    deleting_at timestamptz,
    CHECK ((deleting AND deleting_at IS NOT NULL) OR (NOT deleting AND deleting_at IS NULL)),
    CHECK (width::bigint * height::bigint <= 40000000)
);

CREATE INDEX event_image_uploads_unclaimed_cleanup_idx
    ON event_image_uploads(created_at)
    WHERE submission_id IS NULL;

-- +goose Down
DROP TABLE event_image_uploads;
