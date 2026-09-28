-- +goose NO TRANSACTION

-- +goose Up
CREATE INDEX CONCURRENTLY events_title_trgm_idx
    ON events USING gin (title gin_trgm_ops);
CREATE INDEX CONCURRENTLY events_subtitle_trgm_idx
    ON events USING gin (subtitle gin_trgm_ops)
    WHERE subtitle IS NOT NULL;
CREATE INDEX CONCURRENTLY events_description_trgm_idx
    ON events USING gin (description gin_trgm_ops)
    WHERE description IS NOT NULL;
CREATE INDEX CONCURRENTLY venues_name_trgm_idx
    ON venues USING gin (name gin_trgm_ops);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS venues_name_trgm_idx;
DROP INDEX CONCURRENTLY IF EXISTS events_description_trgm_idx;
DROP INDEX CONCURRENTLY IF EXISTS events_subtitle_trgm_idx;
DROP INDEX CONCURRENTLY IF EXISTS events_title_trgm_idx;
