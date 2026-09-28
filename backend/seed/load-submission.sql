-- Load only the small, synthetic submission dataset. This file is intentionally
-- guarded because the fixture is not suitable for a shared or production DB.
BEGIN;

DO $$
BEGIN
    IF current_database() <> 'max_together_submission' THEN
        RAISE EXCEPTION 'submission fixture may only be loaded into max_together_submission (connected to %)', current_database();
    END IF;
END;
$$;

CREATE TEMP TABLE submission_fixture_data ON COMMIT DROP AS
SELECT :'dataset'::jsonb AS doc;

-- The city is shared catalog identity. Preserve an existing row; fail if the
-- fixed UUID is already occupied by a different city.
INSERT INTO cities (id, name, timezone, center_lat, center_lng)
SELECT (doc->'city'->>'id')::uuid,
       doc->'city'->>'name',
       doc->'city'->>'timezone',
       (doc->'city'->>'center_lat')::double precision,
       (doc->'city'->>'center_lng')::double precision
FROM submission_fixture_data
ON CONFLICT (id) DO NOTHING;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM cities
        WHERE id = 'a0f625ee-2154-5a45-8afe-37adf955ec24'::uuid
          AND name = 'Москва'
          AND timezone = 'Europe/Moscow'
    ) THEN
        RAISE EXCEPTION 'submission fixture Moscow city UUID is occupied by a different city or missing';
    END IF;
END;
$$;

INSERT INTO venues (id, city_id, name, address, latitude, longitude, metro, district, venue_type)
SELECT (doc->'venue'->>'id')::uuid,
       (doc->'venue'->>'city_id')::uuid,
       doc->'venue'->>'name',
       doc->'venue'->>'address',
       (doc->'venue'->>'latitude')::double precision,
       (doc->'venue'->>'longitude')::double precision,
       doc->'venue'->>'metro',
       doc->'venue'->>'district',
       doc->'venue'->>'venue_type'
FROM submission_fixture_data
ON CONFLICT (id) DO UPDATE SET
    city_id = EXCLUDED.city_id,
    name = EXCLUDED.name,
    address = EXCLUDED.address,
    latitude = EXCLUDED.latitude,
    longitude = EXCLUDED.longitude,
    metro = EXCLUDED.metro,
    district = EXCLUDED.district,
    venue_type = EXCLUDED.venue_type
WHERE venues.city_id = EXCLUDED.city_id
  AND venues.name = 'Площадка тестового каталога MAX';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM venues
        WHERE id = '4b0fbbd2-c453-5c63-8e16-33d8dd3bdaf4'::uuid
          AND city_id = 'a0f625ee-2154-5a45-8afe-37adf955ec24'::uuid
          AND name = 'Площадка тестового каталога MAX'
    ) THEN
        RAISE EXCEPTION 'submission fixture venue UUID is occupied by an unrelated venue';
    END IF;
END;
$$;

INSERT INTO events (
    id, source, external_id, is_demo, title, subtitle, description, venue_id,
    starts_at, timezone, price_from_minor, currency, ticket_url,
    ticket_available, status, age_rating, indoor, loudness_level,
    published_at, provider_active
)
SELECT (event->>'id')::uuid,
       'submission-fixture',
       event->>'external_id',
       false,
       event->>'title',
       event->>'subtitle',
       event->>'description',
       (event->>'venue_id')::uuid,
       (((CURRENT_DATE + (event->>'offset_days')::integer)::date + (event->>'local_time')::time)
           AT TIME ZONE (event->>'timezone')),
       event->>'timezone',
       (event->>'price_minor')::integer,
       event->>'currency',
       event->>'ticket_url',
       (event->>'ticket_available')::boolean,
       event->>'status',
       event->>'age_rating',
       (event->>'indoor')::boolean,
       event->>'loudness_level',
       now(),
       true
FROM submission_fixture_data
CROSS JOIN LATERAL jsonb_array_elements(doc->'events') AS event
ON CONFLICT (source, external_id) DO UPDATE SET
    title = EXCLUDED.title,
    subtitle = EXCLUDED.subtitle,
    description = EXCLUDED.description,
    venue_id = EXCLUDED.venue_id,
    starts_at = EXCLUDED.starts_at,
    timezone = EXCLUDED.timezone,
    price_from_minor = EXCLUDED.price_from_minor,
    currency = EXCLUDED.currency,
    ticket_url = EXCLUDED.ticket_url,
    ticket_available = EXCLUDED.ticket_available,
    status = EXCLUDED.status,
    age_rating = EXCLUDED.age_rating,
    indoor = EXCLUDED.indoor,
    loudness_level = EXCLUDED.loudness_level,
    is_demo = false,
    provider_active = true,
    published_at = EXCLUDED.published_at,
    updated_at = now()
WHERE events.source = 'submission-fixture'
  AND events.id = EXCLUDED.id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM submission_fixture_data
        CROSS JOIN LATERAL jsonb_array_elements(doc->'events') AS event
        LEFT JOIN events e ON e.id = (event->>'id')::uuid
                          AND e.source = 'submission-fixture'
                          AND e.external_id = event->>'external_id'
        WHERE e.id IS NULL
    ) THEN
        RAISE EXCEPTION 'submission fixture event UUID/source identity conflicts with existing data';
    END IF;
END;
$$;

INSERT INTO event_categories (event_id, category_slug, weight, is_primary)
SELECT (event->>'id')::uuid, event->>'category_slug', 1, true
FROM submission_fixture_data
CROSS JOIN LATERAL jsonb_array_elements(doc->'events') AS event
ON CONFLICT (event_id, category_slug) DO UPDATE SET
    weight = EXCLUDED.weight,
    is_primary = EXCLUDED.is_primary
WHERE EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = EXCLUDED.event_id AND e.source = 'submission-fixture'
);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM submission_fixture_data
        CROSS JOIN LATERAL jsonb_array_elements(doc->'events') AS event
        JOIN event_images i ON i.id = (event->>'image_id')::uuid
        WHERE i.event_id <> (event->>'id')::uuid
    ) THEN
        RAISE EXCEPTION 'submission fixture image UUID is already owned by another event';
    END IF;
END;
$$;

INSERT INTO event_images (id, event_id, url, role, position)
SELECT (event->>'image_id')::uuid,
       (event->>'id')::uuid,
       event->>'image_url',
       'card',
       0
FROM submission_fixture_data
CROSS JOIN LATERAL jsonb_array_elements(doc->'events') AS event
ON CONFLICT (id) DO UPDATE SET
    event_id = EXCLUDED.event_id,
    url = EXCLUDED.url,
    role = EXCLUDED.role,
    position = EXCLUDED.position
WHERE EXISTS (
    SELECT 1 FROM events e
    WHERE e.id = EXCLUDED.event_id AND e.source = 'submission-fixture'
)
AND event_images.event_id = EXCLUDED.event_id;

COMMIT;
