-- Provider ingestion writes one normalized occurrence and its dependent rows
-- inside a caller-owned transaction.

-- name: UpsertProviderVenue :exec
INSERT INTO venues (id, city_id, name, address, latitude, longitude, metro, district, venue_type)
VALUES (
    sqlc.arg('id'), sqlc.arg('city_id'), sqlc.arg('name'), sqlc.arg('address'),
    sqlc.narg('latitude')::double precision, sqlc.narg('longitude')::double precision, sqlc.narg('metro')::text,
    sqlc.narg('district')::text, sqlc.arg('venue_type')
)
ON CONFLICT (id) DO UPDATE SET
    city_id = EXCLUDED.city_id,
    name = EXCLUDED.name,
    address = EXCLUDED.address,
    latitude = EXCLUDED.latitude,
    longitude = EXCLUDED.longitude,
    metro = EXCLUDED.metro,
    district = EXCLUDED.district,
    venue_type = EXCLUDED.venue_type;

-- name: UpsertProviderEvent :one
INSERT INTO events (
    id, source, external_id, source_updated_at, is_demo, title, subtitle, description,
    venue_id, starts_at, ends_at, timezone, price_from_minor, price_to_minor, currency,
    ticket_url, ticket_available, status, age_rating, indoor, loudness_level,
    published_at, provider_active, provider_last_seen_run_id, updated_at
)
VALUES (
    sqlc.arg('id'), sqlc.arg('source'), sqlc.arg('external_id'), sqlc.narg('source_updated_at')::timestamptz,
    false, sqlc.arg('title'), sqlc.narg('subtitle')::text, sqlc.arg('description'),
    sqlc.arg('venue_id'), sqlc.arg('starts_at'), sqlc.narg('ends_at')::timestamptz,
    sqlc.arg('timezone'), sqlc.narg('price_from_minor')::integer,
    sqlc.narg('price_to_minor')::integer, sqlc.arg('currency'),
    sqlc.narg('ticket_url')::text, sqlc.arg('ticket_available'), sqlc.arg('status'),
    sqlc.narg('age_rating')::text, sqlc.narg('indoor')::boolean,
    sqlc.narg('loudness_level')::text, sqlc.narg('published_at')::timestamptz,
    sqlc.arg('provider_active'), sqlc.narg('provider_last_seen_run_id')::uuid, now()
)
ON CONFLICT (source, external_id) DO UPDATE SET
    source_updated_at = EXCLUDED.source_updated_at,
    is_demo = false,
    title = EXCLUDED.title,
    subtitle = EXCLUDED.subtitle,
    description = EXCLUDED.description,
    venue_id = EXCLUDED.venue_id,
    starts_at = EXCLUDED.starts_at,
    ends_at = EXCLUDED.ends_at,
    timezone = EXCLUDED.timezone,
    price_from_minor = EXCLUDED.price_from_minor,
    price_to_minor = EXCLUDED.price_to_minor,
    currency = EXCLUDED.currency,
    ticket_url = EXCLUDED.ticket_url,
    ticket_available = EXCLUDED.ticket_available,
    status = EXCLUDED.status,
    age_rating = EXCLUDED.age_rating,
    indoor = EXCLUDED.indoor,
    loudness_level = EXCLUDED.loudness_level,
    published_at = EXCLUDED.published_at,
    provider_active = EXCLUDED.provider_active,
    provider_last_seen_run_id = coalesce(EXCLUDED.provider_last_seen_run_id, events.provider_last_seen_run_id),
    updated_at = now()
RETURNING id, (xmax = 0) AS inserted;

-- name: CreateProviderSyncRun :one
INSERT INTO provider_sync_runs (id, provider, city_id, window_start, window_end, reconcile_missing)
VALUES (sqlc.arg('id'), sqlc.arg('provider'), sqlc.arg('city_id'), sqlc.arg('window_start'), sqlc.arg('window_end'), sqlc.arg('reconcile_missing'))
RETURNING id;

-- name: GetProviderSyncRunForUpdate :one
SELECT * FROM provider_sync_runs WHERE id = sqlc.arg('id') FOR UPDATE;

-- name: HasNewerProviderSyncRun :one
SELECT EXISTS (
    SELECT 1 FROM provider_sync_runs newer
    WHERE newer.provider = sqlc.arg('provider')
      AND newer.city_id = sqlc.arg('city_id')
      AND newer.started_at > sqlc.arg('started_at')
);

-- name: ReconcileProviderSyncRun :one
WITH current_run AS (
    SELECT id, provider, city_id, started_at, reconcile_missing
    FROM provider_sync_runs
    WHERE provider_sync_runs.id = sqlc.arg('run_id')
), inactivated AS (
    UPDATE events e
    SET provider_active = false, updated_at = now()
    FROM venues v, current_run run
    WHERE run.reconcile_missing
      AND e.venue_id = v.id
      AND e.source = run.provider
      AND v.city_id = run.city_id
      AND e.is_demo = false
      AND e.provider_active = true
      AND e.provider_last_seen_run_id IS DISTINCT FROM run.id
      AND NOT EXISTS (
          SELECT 1
          FROM provider_sync_runs seen_run
          WHERE seen_run.id = e.provider_last_seen_run_id
            AND seen_run.started_at > run.started_at
      )
    RETURNING e.id
)
SELECT count(*)::integer FROM inactivated;

-- name: CompleteProviderSyncRun :exec
UPDATE provider_sync_runs
SET completed_at = clock_timestamp(), state = sqlc.arg('state'),
    pages_fetched = sqlc.arg('pages_fetched'), fetched = sqlc.arg('fetched'),
    matched = sqlc.arg('matched'), normalized = sqlc.arg('normalized'),
    inserted = sqlc.arg('inserted'), updated = sqlc.arg('updated'),
    skipped = sqlc.arg('skipped'), errors = sqlc.arg('errors'),
    reconciled = sqlc.arg('reconciled'), error_text = sqlc.narg('error_text')::text
WHERE id = sqlc.arg('id') AND state = 'running';

-- name: ClearProviderEventCategories :exec
DELETE FROM event_categories WHERE event_id = sqlc.arg('event_id');

-- name: InsertProviderEventCategory :exec
INSERT INTO event_categories (event_id, category_slug, weight, is_primary)
VALUES (sqlc.arg('event_id'), sqlc.arg('category_slug'), sqlc.arg('weight'), sqlc.arg('is_primary'));

-- name: ClearProviderEventImages :exec
DELETE FROM event_images WHERE event_id = sqlc.arg('event_id');

-- name: InsertProviderEventImage :exec
INSERT INTO event_images (id, event_id, url, width, height, role, position)
VALUES (
    sqlc.arg('id'), sqlc.arg('event_id'), sqlc.arg('url'), sqlc.narg('width')::integer,
    sqlc.narg('height')::integer, sqlc.arg('role'), sqlc.arg('position')
);
