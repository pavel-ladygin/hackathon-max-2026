-- Discovery projections deliberately omit events.ticket_url. These queries are
-- Generated sqlc methods are consumed by internal/discovery.

-- name: SearchDiscoveryEventCards :many
WITH base AS (
    SELECT e.id, e.title, e.subtitle, e.description, e.starts_at, e.timezone, e.price_from_minor,
           e.currency, v.name AS venue_name,
           e.starts_at AT TIME ZONE e.timezone AS local_starts_at,
           CASE WHEN sqlc.narg('latitude')::double precision IS NULL THEN NULL
                ELSE 6371000.0 * 2 * asin(sqrt(least(1.0,
                    power(sin(radians(v.latitude - sqlc.narg('latitude')::double precision) / 2), 2) +
                    cos(radians(sqlc.narg('latitude')::double precision)) * cos(radians(v.latitude)) *
                    power(sin(radians(v.longitude - sqlc.narg('longitude')::double precision) / 2), 2)
                ))) END AS distance_m
    FROM events e JOIN venues v ON v.id = e.venue_id
    WHERE v.city_id = sqlc.arg('city_id') AND e.status = 'published'
), filtered AS (
    SELECT b.* FROM base b
    WHERE (sqlc.narg('query')::text IS NULL OR
           (setweight(to_tsvector('simple', b.title || ' ' || coalesce(b.subtitle, '') || ' ' || b.venue_name), 'A') ||
           setweight(to_tsvector('simple', b.description), 'C')) @@ websearch_to_tsquery('simple', sqlc.narg('query')::text) OR
           similarity(concat_ws(' ', b.title, b.subtitle, b.venue_name), sqlc.narg('query')::text) > 0.1 OR
           concat_ws(' ', b.title, b.subtitle, b.venue_name, b.description) ILIKE '%' || sqlc.narg('query')::text || '%')
      AND (sqlc.narg('date_from')::date IS NULL OR b.local_starts_at::date >= sqlc.narg('date_from')::date)
      AND (sqlc.narg('date_to')::date IS NULL OR b.local_starts_at::date <= sqlc.narg('date_to')::date)
      AND (cardinality(sqlc.arg('day_types')::text[]) = 0 OR CASE WHEN extract(isodow FROM b.local_starts_at) IN (6, 7) THEN 'weekend' ELSE 'weekday' END = ANY(sqlc.arg('day_types')::text[]))
      AND (cardinality(sqlc.arg('time_slots')::text[]) = 0 OR CASE WHEN extract(hour FROM b.local_starts_at) >= 6 AND extract(hour FROM b.local_starts_at) < 12 THEN 'morning' WHEN extract(hour FROM b.local_starts_at) >= 12 AND extract(hour FROM b.local_starts_at) < 17 THEN 'day' WHEN extract(hour FROM b.local_starts_at) >= 17 AND extract(hour FROM b.local_starts_at) < 22 THEN 'evening' ELSE 'night' END = ANY(sqlc.arg('time_slots')::text[]))
      AND (cardinality(sqlc.arg('category_slugs')::text[]) = 0 OR EXISTS (SELECT 1 FROM event_categories ec WHERE ec.event_id = b.id AND ec.category_slug = ANY(sqlc.arg('category_slugs')::text[])))
      AND (sqlc.narg('price_max_minor')::integer IS NULL OR (b.price_from_minor IS NOT NULL AND b.price_from_minor <= sqlc.narg('price_max_minor')::integer))
      AND (NOT sqlc.arg('free_only')::boolean OR b.price_from_minor = 0)
      AND (sqlc.narg('distance_meters')::integer IS NULL OR b.distance_m <= sqlc.narg('distance_meters')::integer)
      AND EXISTS (SELECT 1 FROM event_categories ec WHERE ec.event_id = b.id AND ec.is_primary)
)
SELECT f.id, f.title, f.subtitle,
       (SELECT ec.category_slug FROM event_categories ec WHERE ec.event_id = f.id AND ec.is_primary) AS category_slug,
       f.starts_at, f.timezone, f.venue_name, f.distance_m, f.price_from_minor, f.currency,
       coalesce(image.url, '') AS image_url,
       EXISTS (SELECT 1 FROM saved_events se WHERE se.user_id = sqlc.arg('user_id') AND se.event_id = f.id) AS saved
FROM filtered f
LEFT JOIN LATERAL (
    SELECT ei.url FROM event_images ei WHERE ei.event_id = f.id
    ORDER BY CASE ei.role WHEN 'card' THEN 0 WHEN 'hero' THEN 1 ELSE 2 END, ei.position, ei.id LIMIT 1
) image ON true
WHERE (sqlc.narg('cursor_starts_at')::timestamptz IS NULL OR (f.starts_at, f.id) > (sqlc.narg('cursor_starts_at')::timestamptz, sqlc.narg('cursor_event_id')::uuid))
ORDER BY f.starts_at, f.id
LIMIT sqlc.arg('limit_count');

-- name: CountDiscoveryEventCards :one
WITH base AS (
    SELECT e.id, e.title, e.subtitle, e.description, e.starts_at AT TIME ZONE e.timezone AS local_starts_at, e.price_from_minor, v.name AS venue_name,
           CASE WHEN sqlc.narg('latitude')::double precision IS NULL THEN NULL ELSE 6371000.0 * 2 * asin(sqrt(least(1.0, power(sin(radians(v.latitude - sqlc.narg('latitude')::double precision) / 2), 2) + cos(radians(sqlc.narg('latitude')::double precision)) * cos(radians(v.latitude)) * power(sin(radians(v.longitude - sqlc.narg('longitude')::double precision) / 2), 2)))) END AS distance_m
    FROM events e JOIN venues v ON v.id = e.venue_id WHERE v.city_id = sqlc.arg('city_id') AND e.status = 'published'
)
SELECT count(*)::integer FROM base b
WHERE (sqlc.narg('query')::text IS NULL OR
       (setweight(to_tsvector('simple', b.title || ' ' || coalesce(b.subtitle, '') || ' ' || b.venue_name), 'A') || setweight(to_tsvector('simple', b.description), 'C')) @@ websearch_to_tsquery('simple', sqlc.narg('query')::text) OR
       similarity(concat_ws(' ', b.title, b.subtitle, b.venue_name), sqlc.narg('query')::text) > 0.1 OR concat_ws(' ', b.title, b.subtitle, b.venue_name, b.description) ILIKE '%' || sqlc.narg('query')::text || '%')
  AND (sqlc.narg('date_from')::date IS NULL OR b.local_starts_at::date >= sqlc.narg('date_from')::date) AND (sqlc.narg('date_to')::date IS NULL OR b.local_starts_at::date <= sqlc.narg('date_to')::date)
  AND (cardinality(sqlc.arg('day_types')::text[]) = 0 OR CASE WHEN extract(isodow FROM b.local_starts_at) IN (6, 7) THEN 'weekend' ELSE 'weekday' END = ANY(sqlc.arg('day_types')::text[]))
  AND (cardinality(sqlc.arg('time_slots')::text[]) = 0 OR CASE WHEN extract(hour FROM b.local_starts_at) >= 6 AND extract(hour FROM b.local_starts_at) < 12 THEN 'morning' WHEN extract(hour FROM b.local_starts_at) >= 12 AND extract(hour FROM b.local_starts_at) < 17 THEN 'day' WHEN extract(hour FROM b.local_starts_at) >= 17 AND extract(hour FROM b.local_starts_at) < 22 THEN 'evening' ELSE 'night' END = ANY(sqlc.arg('time_slots')::text[]))
  AND (cardinality(sqlc.arg('category_slugs')::text[]) = 0 OR EXISTS (SELECT 1 FROM event_categories ec WHERE ec.event_id = b.id AND ec.category_slug = ANY(sqlc.arg('category_slugs')::text[])))
  AND (sqlc.narg('price_max_minor')::integer IS NULL OR (b.price_from_minor IS NOT NULL AND b.price_from_minor <= sqlc.narg('price_max_minor')::integer)) AND (NOT sqlc.arg('free_only')::boolean OR b.price_from_minor = 0)
  AND (sqlc.narg('distance_meters')::integer IS NULL OR b.distance_m <= sqlc.narg('distance_meters')::integer)
  AND EXISTS (SELECT 1 FROM event_categories ec WHERE ec.event_id = b.id AND ec.is_primary);

-- name: GetDiscoveryEventDetail :one
SELECT e.id, e.title, e.subtitle,
       (SELECT ec.category_slug FROM event_categories ec WHERE ec.event_id = e.id AND ec.is_primary) AS category_slug,
       e.starts_at, e.timezone, v.name,
       CASE WHEN sqlc.narg('latitude')::double precision IS NULL THEN NULL ELSE 6371000.0 * 2 * asin(sqrt(least(1.0, power(sin(radians(v.latitude - sqlc.narg('latitude')::double precision) / 2), 2) + cos(radians(sqlc.narg('latitude')::double precision)) * cos(radians(v.latitude)) * power(sin(radians(v.longitude - sqlc.narg('longitude')::double precision) / 2), 2)))) END,
       e.price_from_minor, e.currency, coalesce(image.url, '') AS image_url,
       EXISTS (SELECT 1 FROM saved_events se WHERE se.user_id = sqlc.arg('user_id') AND se.event_id = e.id),
       e.description, e.ends_at, v.id, v.address, v.latitude, v.longitude, v.metro, v.district,
       e.ticket_available, e.status, e.age_rating, e.source, e.source_updated_at, e.is_demo
FROM events e JOIN venues v ON v.id = e.venue_id
LEFT JOIN LATERAL (
    SELECT ei.url FROM event_images ei WHERE ei.event_id = e.id
    ORDER BY CASE ei.role WHEN 'card' THEN 0 WHEN 'hero' THEN 1 ELSE 2 END, ei.position, ei.id LIMIT 1
) image ON true
WHERE e.id = sqlc.arg('event_id') AND EXISTS (SELECT 1 FROM event_categories ec WHERE ec.event_id = e.id AND ec.is_primary);

-- name: ListDiscoveryEventImages :many
SELECT url, width, height, role FROM event_images WHERE event_id = sqlc.arg('event_id') ORDER BY CASE role WHEN 'hero' THEN 0 WHEN 'card' THEN 1 ELSE 2 END, position, id LIMIT 50;

-- name: GetDiscoveryUserCity :one
SELECT city_id FROM users WHERE id = sqlc.arg('user_id') AND city_id IS NOT NULL;
