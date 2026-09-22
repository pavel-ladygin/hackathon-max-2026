-- +goose Up
ALTER TABLE venues
    ALTER COLUMN latitude DROP NOT NULL,
    ALTER COLUMN longitude DROP NOT NULL,
    ADD CONSTRAINT venues_coordinates_pair_check
        CHECK ((latitude IS NULL) = (longitude IS NULL));

-- +goose Down
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM venues WHERE latitude IS NULL OR longitude IS NULL) THEN
        RAISE EXCEPTION 'cannot restore required venue coordinates while coordinate-less venues exist';
    END IF;
END $$;

ALTER TABLE venues
    DROP CONSTRAINT venues_coordinates_pair_check,
    ALTER COLUMN latitude SET NOT NULL,
    ALTER COLUMN longitude SET NOT NULL;
