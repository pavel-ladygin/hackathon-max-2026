-- +goose Up
CREATE UNIQUE INDEX event_one_primary_category ON event_categories(event_id) WHERE is_primary;
CREATE INDEX events_discovery_idx ON events(status, starts_at);
CREATE INDEX events_venue_start_idx ON events(venue_id, starts_at);
CREATE INDEX event_categories_slug_idx ON event_categories(category_slug, event_id);
CREATE UNIQUE INDEX room_one_creator ON room_members(room_id) WHERE role='creator';
CREATE UNIQUE INDEX user_one_active_room ON room_members(user_id) WHERE is_active;
CREATE INDEX room_votes_match_idx ON room_votes(pool_id,event_id,vote);
CREATE UNIQUE INDEX behavior_client_dedupe ON behavior_events(user_id,client_event_id) WHERE client_event_id IS NOT NULL;

-- +goose Down
DROP INDEX behavior_client_dedupe;
DROP INDEX room_votes_match_idx;
DROP INDEX user_one_active_room;
DROP INDEX room_one_creator;
DROP INDEX event_categories_slug_idx;
DROP INDEX events_venue_start_idx;
DROP INDEX events_discovery_idx;
DROP INDEX event_one_primary_category;
