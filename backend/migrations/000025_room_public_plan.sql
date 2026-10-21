-- +goose Up
ALTER TABLE rooms
    ADD COLUMN public_plan jsonb,
    ADD CONSTRAINT rooms_public_plan_object_check
        CHECK (public_plan IS NULL OR jsonb_typeof(public_plan) = 'object');

-- +goose Down
ALTER TABLE rooms
    DROP CONSTRAINT rooms_public_plan_object_check,
    DROP COLUMN public_plan;
