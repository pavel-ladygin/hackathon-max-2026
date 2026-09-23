-- +goose Up
CREATE TABLE provider_sync_runs (
    id uuid PRIMARY KEY,
    provider text NOT NULL CHECK (provider <> '' AND provider <> 'demo'),
    city_id uuid NOT NULL REFERENCES cities(id),
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    state text NOT NULL DEFAULT 'running'
        CHECK (state IN ('running','succeeded','failed','cancelled')),
    pages_fetched integer NOT NULL DEFAULT 0 CHECK (pages_fetched >= 0),
    fetched integer NOT NULL DEFAULT 0 CHECK (fetched >= 0),
    matched integer NOT NULL DEFAULT 0 CHECK (matched >= 0),
    normalized integer NOT NULL DEFAULT 0 CHECK (normalized >= 0),
    inserted integer NOT NULL DEFAULT 0 CHECK (inserted >= 0),
    updated integer NOT NULL DEFAULT 0 CHECK (updated >= 0),
    skipped integer NOT NULL DEFAULT 0 CHECK (skipped >= 0),
    errors integer NOT NULL DEFAULT 0 CHECK (errors >= 0),
    reconciled integer NOT NULL DEFAULT 0 CHECK (reconciled >= 0),
    error_text text,
    CHECK (window_end >= window_start),
    CHECK ((state = 'running') = (completed_at IS NULL))
);

ALTER TABLE events
    ADD COLUMN provider_active boolean NOT NULL DEFAULT true,
    ADD COLUMN provider_last_seen_run_id uuid REFERENCES provider_sync_runs(id);

CREATE INDEX provider_sync_runs_scope_started_idx
    ON provider_sync_runs (provider, city_id, started_at DESC);
CREATE INDEX events_provider_last_seen_run_idx
    ON events (provider_last_seen_run_id)
    WHERE provider_last_seen_run_id IS NOT NULL;
CREATE INDEX events_active_provider_idx
    ON events (source, starts_at)
    WHERE provider_active = true AND is_demo = false;

-- +goose Down
DROP INDEX events_active_provider_idx;
DROP INDEX events_provider_last_seen_run_idx;
DROP INDEX provider_sync_runs_scope_started_idx;
ALTER TABLE events
    DROP COLUMN provider_last_seen_run_id,
    DROP COLUMN provider_active;
DROP TABLE provider_sync_runs;
