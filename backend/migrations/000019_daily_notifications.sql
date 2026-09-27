-- +goose Up
ALTER TABLE users
    ADD COLUMN daily_notifications_enabled boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN daily_notifications_enabled;
