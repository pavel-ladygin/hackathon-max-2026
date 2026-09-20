-- +goose Up
CREATE TABLE venues (
    id uuid PRIMARY KEY, city_id uuid NOT NULL REFERENCES cities(id), name text NOT NULL,
    address text NOT NULL, latitude double precision NOT NULL, longitude double precision NOT NULL,
    metro text, district text, venue_type text NOT NULL DEFAULT 'other'
        CHECK (venue_type IN ('nightclub','theatre','cinema','museum','stadium','cafe','restaurant','concert_hall','outdoor','other'))
);
CREATE TABLE events (
    id uuid PRIMARY KEY, source text NOT NULL, external_id text NOT NULL, source_updated_at timestamptz,
    is_demo boolean NOT NULL DEFAULT false, title text NOT NULL, subtitle text, description text NOT NULL,
    venue_id uuid NOT NULL REFERENCES venues(id), starts_at timestamptz NOT NULL, ends_at timestamptz,
    timezone text NOT NULL, price_from_minor integer CHECK (price_from_minor >= 0),
    price_to_minor integer CHECK (price_to_minor >= 0), currency char(3) NOT NULL DEFAULT 'RUB',
    ticket_url text, ticket_available boolean NOT NULL DEFAULT false,
    status text NOT NULL CHECK (status IN ('published','sold_out','cancelled')),
    age_rating text CHECK (age_rating IS NULL OR age_rating IN ('0+','6+','12+','16+','18+','unknown')),
    indoor boolean, loudness_level text CHECK (loudness_level IS NULL OR loudness_level IN ('quiet','normal','loud','very_loud')),
    published_at timestamptz, updated_at timestamptz NOT NULL DEFAULT now(), UNIQUE(source, external_id)
);
CREATE TABLE event_categories (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE, category_slug text NOT NULL,
    weight real NOT NULL DEFAULT 1, is_primary boolean NOT NULL DEFAULT false,
    PRIMARY KEY(event_id, category_slug)
);
CREATE TABLE event_images (
    id uuid PRIMARY KEY, event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    url text NOT NULL, width integer, height integer, role text NOT NULL DEFAULT 'card'
        CHECK (role IN ('card','hero','gallery')), position integer NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE event_images;
DROP TABLE event_categories;
DROP TABLE events;
DROP TABLE venues;
