-- +goose Up
CREATE TABLE user_preferences (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    budget_max_minor integer NOT NULL CHECK (budget_max_minor >= 0), usual_day_types text[] NOT NULL DEFAULT '{}',
    usual_time_slots text[] NOT NULL DEFAULT '{}', version integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE user_category_preferences (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, category_slug text NOT NULL,
    weight real NOT NULL DEFAULT 1, source text NOT NULL, updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, category_slug)
);

-- +goose Down
DROP TABLE user_category_preferences;
DROP TABLE user_preferences;
