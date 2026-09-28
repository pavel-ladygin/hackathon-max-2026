-- +goose Up
CREATE TABLE event_sources (
    id uuid PRIMARY KEY,
    source_key text NOT NULL UNIQUE CHECK (source_key <> '' AND source_key <> 'demo'),
    name text NOT NULL CHECK (name <> ''),
    enabled boolean NOT NULL DEFAULT true,
    endpoint_url text NOT NULL,
    auth_type text NOT NULL DEFAULT 'none'
        CHECK (auth_type IN ('none','bearer','api_key_header','api_key_query')),
    auth_name text NOT NULL DEFAULT '',
    auth_secret_ciphertext bytea,
    key_version smallint NOT NULL CHECK (key_version > 0),
    query_config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(query_config) = 'object'),
    response_path text NOT NULL DEFAULT '',
    mapping_config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(mapping_config) = 'object'),
    transform_config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(transform_config) = 'object'),
    pagination_config jsonb NOT NULL DEFAULT '{"mode":"none"}'::jsonb CHECK (jsonb_typeof(pagination_config) = 'object'),
    default_category text NOT NULL,
    default_timezone text NOT NULL,
    default_currency text NOT NULL,
    default_status text NOT NULL,
    price_unit text NOT NULL DEFAULT 'major' CHECK (price_unit IN ('major','minor')),
    mapping_locked boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (auth_type = 'none' OR auth_secret_ciphertext IS NOT NULL)
);

-- +goose StatementBegin
CREATE FUNCTION prevent_event_source_identity_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.source_key <> OLD.source_key THEN
        RAISE EXCEPTION 'event source identity is immutable';
    END IF;
    IF OLD.mapping_locked AND NEW.mapping_config->'external_id' IS DISTINCT FROM OLD.mapping_config->'external_id' THEN
        RAISE EXCEPTION 'event source mapping is immutable after import';
    END IF;
    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER event_sources_identity_guard
BEFORE UPDATE ON event_sources
FOR EACH ROW EXECUTE FUNCTION prevent_event_source_identity_change();

ALTER TABLE provider_sync_runs
    ADD COLUMN reconcile_missing boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE provider_sync_runs DROP COLUMN reconcile_missing;
DROP TRIGGER event_sources_identity_guard ON event_sources;
DROP FUNCTION prevent_event_source_identity_change();
DROP TABLE event_sources;
