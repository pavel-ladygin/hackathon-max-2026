-- Additive metadata only. Operator must check existing city identities before rollout.
-- UUID v5 URL namespace: https://max-together.local/cities/saint-petersburg
-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE cities IN SHARE ROW EXCLUSIVE MODE;
    IF EXISTS (
        SELECT 1 FROM cities
        WHERE lower(translate(btrim(name), 'САНКТПЕРБУГ', 'санктпербуг'))
            IN ('санкт-петербург', 'санкт петербург', 'спб', 'saint petersburg', 'saint-petersburg', 'st. petersburg', 'st petersburg')
          AND id <> '19096037-cba9-55e0-8a99-322ce61554a4'
    ) THEN
        RAISE EXCEPTION 'Saint Petersburg exists with another ID; operator integration decision required';
    END IF;
    IF EXISTS (
        SELECT 1 FROM cities WHERE id='19096037-cba9-55e0-8a99-322ce61554a4'
          AND (name <> 'Санкт-Петербург' OR timezone <> 'Europe/Moscow'
               OR abs(center_lat-59.9386)>0.05 OR abs(center_lng-30.3141)>0.05)
    ) THEN
        RAISE EXCEPTION 'Reserved Saint Petersburg ID has conflicting metadata; operator integration decision required';
    END IF;
    INSERT INTO cities(id,name,timezone,center_lat,center_lng)
    VALUES ('19096037-cba9-55e0-8a99-322ce61554a4','Санкт-Петербург','Europe/Moscow',59.9386,30.3141)
    ON CONFLICT (id) DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Retain referenced additive metadata. Roll back flags/application, never catalog rows.
SELECT 1;
