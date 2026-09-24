-- Geographic viewport searches need to narrow candidates by city and venue coordinates.
-- +goose Up
CREATE INDEX venues_city_coordinates_idx
    ON venues (city_id, latitude, longitude)
    WHERE latitude IS NOT NULL AND longitude IS NOT NULL;

-- +goose Down
DROP INDEX venues_city_coordinates_idx;
