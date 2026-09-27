-- +goose Up
ALTER TABLE rooms DROP CONSTRAINT rooms_state_check;
ALTER TABLE rooms ADD CONSTRAINT rooms_state_check
  CHECK (state IN ('collecting_intents','ranking','voting','matched','exhausted','closed'));
ALTER TABLE rooms ADD COLUMN closed_by uuid REFERENCES users(id),
  ADD COLUMN closed_at timestamptz,
  ADD CONSTRAINT rooms_closed_metadata_check CHECK (
    (state = 'closed' AND closed_by IS NOT NULL AND closed_at IS NOT NULL)
    OR (state <> 'closed' AND closed_by IS NULL AND closed_at IS NULL)
  );

CREATE TABLE room_close_notices (
  room_id uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  recipient_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  acknowledged_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (room_id, recipient_user_id)
);
CREATE INDEX room_close_notices_unread_idx
  ON room_close_notices (recipient_user_id, created_at DESC)
  WHERE acknowledged_at IS NULL;

-- +goose Down
DROP INDEX room_close_notices_unread_idx;
DROP TABLE room_close_notices;
ALTER TABLE rooms DROP CONSTRAINT rooms_closed_metadata_check;
ALTER TABLE rooms DROP COLUMN closed_by, DROP COLUMN closed_at;
ALTER TABLE rooms DROP CONSTRAINT rooms_state_check;
ALTER TABLE rooms ADD CONSTRAINT rooms_state_check
  CHECK (state IN ('collecting_intents','ranking','voting','matched','exhausted'));
