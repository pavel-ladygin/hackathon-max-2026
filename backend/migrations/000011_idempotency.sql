-- +goose Up
CREATE TABLE idempotency_records (
    user_id uuid NOT NULL REFERENCES users(id), key text NOT NULL, route text NOT NULL,
    request_hash text NOT NULL, response_status integer NOT NULL, response_body jsonb NOT NULL,
    expires_at timestamptz NOT NULL, PRIMARY KEY(user_id,key,route)
);

-- +goose Down
DROP TABLE idempotency_records;
