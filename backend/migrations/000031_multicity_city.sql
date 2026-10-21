-- Metadata source: https://www.geonames.org/551487/kazan.html (2026-10-20).
-- Applied after the released 000025/000030 migrations. No event rows are inserted here.
-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE cities IN SHARE ROW EXCLUSIVE MODE;
    IF EXISTS (
        SELECT 1 FROM cities
        -- C-locale databases do not lowercase Cyrillic; normalize the
        -- letters in Казань explicitly before the Latin case fold.
        WHERE lower(translate(btrim(name), 'КАЗНЬ', 'казнь')) IN ('казань', 'kazan', 'kazan''', 'qazan')
          AND id <> '3d3c83de-691b-510e-85e5-79685c4012ee'
    ) THEN
        RAISE EXCEPTION 'Kazan already exists with a different city ID; integration decision required';
    END IF;
    IF EXISTS (
        SELECT 1 FROM cities WHERE id='3d3c83de-691b-510e-85e5-79685c4012ee'
          AND (lower(translate(btrim(name), 'КАЗНЬ', 'казнь')) NOT IN ('казань','kazan','kazan''','qazan')
               OR timezone <> 'Europe/Moscow'
               OR abs(center_lat-55.78874)>0.05 OR abs(center_lng-49.12214)>0.05)
    ) THEN
        RAISE EXCEPTION 'Reserved Kazan city ID has conflicting metadata; integration decision required';
    END IF;
    INSERT INTO cities (id,name,timezone,center_lat,center_lng)
    VALUES ('3d3c83de-691b-510e-85e5-79685c4012ee','Казань','Europe/Moscow',55.78874,49.12214)
    ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Deliberately retain additive city metadata: it may already be referenced by
-- venues/users/rooms. Roll back application/config, never delete catalog data.
SELECT 1;
