-- +goose Up
-- Keep the original analytics_room_metrics view intact for existing consumers.
-- This view adds invite confirmation and de-duplicated room-level funnel facts.
CREATE VIEW analytics_room_funnel_metrics AS
SELECT rm.room_id,
       rm.creator_user_id,
       rm.created_at,
       rm.first_joined_at,
       rm.activated_at,
       rm.matched_at,
       rm.matched_event_id,
       rm.state,
       (rm.invite_shared OR EXISTS (
           SELECT 1
           FROM room_invites ri
           JOIN room_members member ON member.room_id = ri.room_id AND member.user_id = ri.consumed_by
           WHERE ri.room_id = rm.room_id
             AND ri.consumed_at IS NOT NULL
             AND ri.consumed_by IS NOT NULL
             AND member.user_id <> rm.creator_user_id
       )) AS invite_confirmed,
       (rm.first_joined_at IS NOT NULL) AS joined,
       (rm.activated_at IS NOT NULL) AS activated,
       (rm.matched_at IS NOT NULL) AS matched,
       rm.additional_members,
       rm.invite_opened,
       rm.match_shown,
       EXISTS (SELECT 1 FROM analytics_events event
         WHERE event.room_id = rm.room_id AND event.event_name = 'ticket_click'
           AND rm.matched_at IS NOT NULL AND event.occurred_at >= rm.matched_at
           AND event.event_id = rm.matched_event_id) AS ticket_clicked,
       (SELECT count(DISTINCT vote.event_id)::bigint FROM room_votes vote
         WHERE vote.room_id = rm.room_id
           AND vote.created_at <= coalesce(rm.matched_at, 'infinity'::timestamptz)) AS swipe_count,
       (SELECT count(*)::bigint FROM analytics_events event
         WHERE event.room_id = rm.room_id AND event.event_name = 'ticket_click'
           AND rm.matched_at IS NOT NULL AND event.occurred_at >= rm.matched_at
           AND event.event_id = rm.matched_event_id) AS ticket_clicks,
       EXISTS (SELECT 1 FROM analytics_events event
         WHERE event.room_id = rm.room_id AND event.event_name = 'match_opened'
           AND rm.matched_at IS NOT NULL AND event.occurred_at >= rm.matched_at
           AND event.event_id = rm.matched_event_id) AS match_event_opened
FROM analytics_room_metrics rm;

-- +goose Down
DROP VIEW analytics_room_funnel_metrics;
