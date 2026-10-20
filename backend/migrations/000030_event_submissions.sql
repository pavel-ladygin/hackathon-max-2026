-- +goose Up
ALTER TABLE event_sources
    ADD COLUMN source_kind text NOT NULL DEFAULT 'generic'
        CHECK (source_kind IN ('generic','system')),
    ALTER COLUMN endpoint_url DROP NOT NULL;

ALTER TABLE event_sources ADD CONSTRAINT event_sources_endpoint_kind_check
    CHECK ((source_kind = 'generic' AND endpoint_url IS NOT NULL AND source_key <> 'user')
        OR (source_kind = 'system' AND endpoint_url IS NULL AND source_key = 'user'));

INSERT INTO event_sources (
    id, source_key, name, enabled, endpoint_url, auth_type, auth_name,
    auth_secret_ciphertext, key_version, default_category, default_timezone,
    default_currency, default_status, source_kind, mapping_locked
) VALUES (
    '11111111-1111-5111-8111-111111111130', 'user', 'User submitted events', true,
    NULL, 'none', '', NULL, 1, 'other', 'UTC', 'RUB', 'published', 'system', true
);

CREATE TABLE event_submissions (
    id uuid PRIMARY KEY,
    author_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    city_id uuid NOT NULL REFERENCES cities(id),
    starts_at timestamptz NOT NULL,
    timezone text NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    decided_at timestamptz,
    decided_by text,
    rejection_reason text,
    event_id uuid UNIQUE REFERENCES events(id),
    CHECK ((status = 'pending' AND decided_at IS NULL AND decided_by IS NULL AND rejection_reason IS NULL AND event_id IS NULL)
        OR (status = 'approved' AND decided_at IS NOT NULL AND decided_by IS NOT NULL AND rejection_reason IS NULL AND event_id IS NOT NULL)
        OR (status = 'rejected' AND decided_at IS NOT NULL AND decided_by IS NOT NULL AND length(btrim(rejection_reason)) > 0 AND event_id IS NULL)),
    UNIQUE (author_id, idempotency_key)
);

CREATE INDEX event_submissions_queue_idx ON event_submissions(status, created_at DESC);
CREATE INDEX event_submissions_author_idx ON event_submissions(author_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION guard_event_submission_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status <> 'pending' OR NEW.status NOT IN ('approved','rejected') THEN
        RAISE EXCEPTION 'event submission status transition is not allowed';
    END IF;
    IF NEW.id <> OLD.id OR NEW.author_id <> OLD.author_id OR NEW.city_id <> OLD.city_id
       OR NEW.starts_at <> OLD.starts_at OR NEW.timezone <> OLD.timezone
       OR NEW.payload <> OLD.payload OR NEW.idempotency_key <> OLD.idempotency_key
       OR NEW.request_hash <> OLD.request_hash OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'event submission contents are immutable';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER event_submissions_transition_guard
BEFORE UPDATE ON event_submissions
FOR EACH ROW EXECUTE FUNCTION guard_event_submission_transition();

-- +goose Down
-- Refuse to erase published user events; operators must preserve them explicitly.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM events WHERE source = 'user') THEN
        RAISE EXCEPTION 'cannot roll back user event submissions while published user events exist';
    END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER event_submissions_transition_guard ON event_submissions;
DROP FUNCTION guard_event_submission_transition();
DROP TABLE event_submissions;
DELETE FROM event_sources WHERE source_key = 'user';
ALTER TABLE event_sources DROP CONSTRAINT event_sources_endpoint_kind_check;
ALTER TABLE event_sources DROP COLUMN source_kind;
ALTER TABLE event_sources ALTER COLUMN endpoint_url SET NOT NULL;
