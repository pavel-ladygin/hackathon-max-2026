-- +goose Up
CREATE TABLE cities (
    id uuid PRIMARY KEY, name text NOT NULL, timezone text NOT NULL,
    center_lat double precision NOT NULL, center_lng double precision NOT NULL
);
CREATE TABLE metro_stations (
    id uuid PRIMARY KEY, city_id uuid NOT NULL REFERENCES cities(id), name text NOT NULL,
    latitude double precision NOT NULL, longitude double precision NOT NULL
);
CREATE INDEX metro_stations_city_idx ON metro_stations(city_id);

-- +goose Down
DROP TABLE metro_stations;
DROP TABLE cities;
