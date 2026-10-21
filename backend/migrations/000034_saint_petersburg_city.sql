-- UUID v5 URL namespace, name: max-together:city:saint-petersburg.
-- Additive city metadata only; operator must resolve any existing identity conflict.
-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE cities IN SHARE ROW EXCLUSIVE MODE;
    IF EXISTS (
        SELECT 1 FROM cities
        WHERE lower(translate(btrim(name), 'САНКТПЕРБУГ', 'санктпербуг'))
              IN ('санкт-петербург', 'санкт петербург', 'спб', 'saint petersburg', 'saint-petersburg', 'st. petersburg')
          AND id <> 'bf61fbf5-8c5b-53da-96c7-3560df3f8177'
    ) THEN
        RAISE EXCEPTION 'Saint Petersburg already exists with another city ID; resolve identity before migration';
    END IF;
    IF EXISTS (
        SELECT 1 FROM cities WHERE id='bf61fbf5-8c5b-53da-96c7-3560df3f8177'
          AND (name <> 'Санкт-Петербург' OR timezone <> 'Europe/Moscow'
               OR abs(center_lat-59.939095)>0.05 OR abs(center_lng-30.315868)>0.05)
    ) THEN
        RAISE EXCEPTION 'Saint Petersburg city ID has conflicting metadata';
    END IF;
    INSERT INTO cities(id,name,timezone,center_lat,center_lng)
    VALUES ('bf61fbf5-8c5b-53da-96c7-3560df3f8177','Санкт-Петербург','Europe/Moscow',59.939095,30.315868)
    ON CONFLICT (id) DO NOTHING;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Keep city metadata that may already have users, venues or rooms referencing it.
SELECT 1;
