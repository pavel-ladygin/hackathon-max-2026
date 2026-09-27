-- +goose Up
CREATE TABLE daily_notification_deliveries (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  delivery_date date NOT NULL,
  status text NOT NULL DEFAULT 'sending' CHECK (status IN ('sending','sent','failed','unknown','rejected')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error text,
  sent_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, delivery_date),
  CHECK ((status = 'sent') = (sent_at IS NOT NULL))
);
CREATE INDEX daily_notification_deliveries_retry_idx
  ON daily_notification_deliveries (delivery_date, updated_at)
  WHERE status IN ('failed','sending');

-- +goose Down
DROP TABLE daily_notification_deliveries;
