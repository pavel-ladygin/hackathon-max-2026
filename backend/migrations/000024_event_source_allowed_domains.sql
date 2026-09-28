-- +goose Up
CREATE TABLE event_source_allowed_domains (
    id uuid PRIMARY KEY,
    source_id uuid NOT NULL REFERENCES event_sources(id) ON DELETE CASCADE,
    hostname text NOT NULL CHECK (
        hostname = lower(hostname) AND length(hostname) <= 253
        AND hostname ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'
        AND hostname !~ '^[0-9.]+$'
    ),
    purpose text NOT NULL CHECK (purpose IN ('image','ticket')),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (source_id, hostname, purpose)
);

CREATE INDEX event_source_allowed_domains_lookup_idx
    ON event_source_allowed_domains (source_id, purpose, hostname)
    WHERE enabled;

-- +goose Down
DROP TABLE event_source_allowed_domains;
