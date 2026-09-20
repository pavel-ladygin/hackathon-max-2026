-- Demo writes belong to the platform store. Called only inside one seed transaction.

-- name: LockDemoSeed :exec
SELECT pg_advisory_xact_lock(768204210);

-- name: SeedCity :exec
INSERT INTO cities (id, name, timezone, center_lat, center_lng)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    timezone = EXCLUDED.timezone,
    center_lat = EXCLUDED.center_lat,
    center_lng = EXCLUDED.center_lng;

-- name: SeedMetroStation :exec
INSERT INTO metro_stations (id, city_id, name, latitude, longitude)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
    city_id = EXCLUDED.city_id,
    name = EXCLUDED.name,
    latitude = EXCLUDED.latitude,
    longitude = EXCLUDED.longitude;

-- name: SeedVenue :exec
INSERT INTO venues (id, city_id, name, address, latitude, longitude, metro, district, venue_type)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
    city_id = EXCLUDED.city_id,
    name = EXCLUDED.name,
    address = EXCLUDED.address,
    latitude = EXCLUDED.latitude,
    longitude = EXCLUDED.longitude,
    metro = EXCLUDED.metro,
    district = EXCLUDED.district,
    venue_type = EXCLUDED.venue_type;

-- name: SeedEvent :execrows
INSERT INTO events (id, source, external_id, source_updated_at, is_demo, title, subtitle, description, venue_id, starts_at, ends_at, timezone, price_from_minor, price_to_minor, currency, ticket_url, ticket_available, status, age_rating, indoor, loudness_level, published_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
ON CONFLICT (id) DO UPDATE SET
    source = EXCLUDED.source,
    external_id = EXCLUDED.external_id,
    source_updated_at = EXCLUDED.source_updated_at,
    is_demo = EXCLUDED.is_demo,
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
    updated_at = EXCLUDED.updated_at
WHERE events.source = 'demo' AND events.is_demo AND events.external_id = EXCLUDED.external_id;

-- name: SeedCategory :exec
INSERT INTO event_categories (event_id, category_slug, weight, is_primary)
VALUES ($1, $2, $3, $4)
ON CONFLICT (event_id, category_slug) DO UPDATE SET
    weight = EXCLUDED.weight,
    is_primary = EXCLUDED.is_primary;

-- name: SeedImage :exec
INSERT INTO event_images (id, event_id, url, width, height, role, position)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
    event_id = EXCLUDED.event_id,
    url = EXCLUDED.url,
    width = EXCLUDED.width,
    height = EXCLUDED.height,
    role = EXCLUDED.role,
    position = EXCLUDED.position;

-- name: ClearDemoCategories :exec
DELETE FROM event_categories WHERE event_id = $1;

-- name: ClearDemoImages :exec
DELETE FROM event_images WHERE event_id = $1;
