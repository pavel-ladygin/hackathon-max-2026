-- +goose Up
ALTER TABLE behavior_events
    ADD COLUMN event_version integer NOT NULL DEFAULT 1 CHECK (event_version > 0),
    ADD COLUMN session_id uuid,
    ADD COLUMN platform text,
    ADD COLUMN app_version text,
    ADD COLUMN entry_point text,
    ADD COLUMN properties jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(properties) = 'object'),
    ADD COLUMN deduplication_key text;

ALTER TABLE behavior_events DROP CONSTRAINT behavior_events_check;
ALTER TABLE behavior_events ADD CONSTRAINT behavior_events_check CHECK (
    origin = 'server' OR type IN (
        'impression','open','share','app_opened','session_started',
        'onboarding_started','onboarding_completed','feed_opened',
        'event_impression','event_opened','search_performed','filters_opened',
        'filters_applied','filters_reset','map_opened','map_marker_opened',
        'room_creation_started','room_opened','invite_opened','invite_shared',
        'invite_link_opened','room_join_started','swipe_session_started',
        'event_swipe_impression','match_shown','match_opened',
        'swipe_pool_exhausted','room_creation_failed','room_join_failed',
        'invite_share_failed','ticket_redirect_failed',
        'client_error','client_performance'
    )
);

CREATE UNIQUE INDEX behavior_deduplication_key_idx
    ON behavior_events (user_id, deduplication_key)
    WHERE deduplication_key IS NOT NULL;
CREATE INDEX behavior_events_session_idx
    ON behavior_events (session_id, occurred_at)
    WHERE session_id IS NOT NULL;
CREATE INDEX behavior_events_type_time_idx
    ON behavior_events (type, occurred_at);
CREATE INDEX behavior_events_occurred_at_idx
    ON behavior_events (occurred_at);
CREATE INDEX behavior_events_room_time_idx
    ON behavior_events (room_id, occurred_at)
    WHERE room_id IS NOT NULL;

-- Call from the deployment's daily maintenance scheduler. Kept as an explicit
-- function because this schema does not assume pg_cron is installed.
CREATE FUNCTION analytics_prune_behavior_events() RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE deleted_count bigint;
BEGIN
    DELETE FROM behavior_events WHERE occurred_at < now() - interval '90 days';
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$;

-- Canonical event names allow existing recommendation events to participate
-- in product metrics without rewriting historical rows.
CREATE VIEW analytics_events AS
SELECT b.*,
       CASE b.type
           WHEN 'impression' THEN 'event_impression'
           WHEN 'open' THEN 'event_opened'
           WHEN 'room_create' THEN 'room_created'
           WHEN 'room_join' THEN 'room_joined'
           WHEN 'intent_submit' THEN 'room_intent_submitted'
           WHEN 'like' THEN 'event_liked'
           WHEN 'dislike' THEN 'event_disliked'
           WHEN 'match' THEN 'match_created'
           WHEN 'save' THEN 'event_saved'
           WHEN 'unsave' THEN 'event_unsaved'
           WHEN 'ticket_click' THEN 'ticket_click'
           ELSE b.type
       END AS event_name
FROM behavior_events b;

-- Activation is a business fact derived from votes: at least two distinct
-- room members have voted. The timestamp is when the second member first voted.
CREATE VIEW analytics_room_activation AS
WITH member_first_votes AS (
    SELECT rv.room_id, rv.user_id, min(rv.created_at) AS first_vote_at
    FROM room_votes rv
    GROUP BY rv.room_id, rv.user_id
), ranked AS (
    SELECT room_id, first_vote_at,
           row_number() OVER (PARTITION BY room_id ORDER BY first_vote_at) AS voter_number
    FROM member_first_votes
)
SELECT room_id, first_vote_at AS activated_at
FROM ranked
WHERE voter_number = 2;

-- One row per room, suitable for the room-level funnel and central metrics.
CREATE VIEW analytics_room_metrics AS
SELECT r.id AS room_id,
       r.creator_user_id,
       r.created_at,
       (SELECT min(rm.joined_at) FROM room_members rm
         WHERE rm.room_id = r.id AND rm.user_id <> r.creator_user_id) AS first_joined_at,
       (SELECT count(*) FROM room_members rm
         WHERE rm.room_id = r.id AND rm.user_id <> r.creator_user_id) AS additional_members,
       a.activated_at,
       m.matched_at,
       m.event_id AS matched_event_id,
       (SELECT count(*) FROM room_votes v
         WHERE v.room_id = r.id AND (m.matched_at IS NULL OR v.created_at <= m.matched_at)) AS swipe_count,
       (SELECT count(*) FROM analytics_events e
         WHERE e.room_id = r.id AND e.event_name = 'invite_shared') > 0 AS invite_shared,
       (SELECT count(*) FROM analytics_events e
         WHERE e.room_id = r.id AND e.event_name IN ('invite_opened','invite_link_opened')) > 0 AS invite_opened,
       (SELECT count(*) FROM analytics_events e
         WHERE e.room_id = r.id AND e.event_name = 'ticket_click'
           AND m.matched_at IS NOT NULL AND e.occurred_at >= m.matched_at) > 0 AS ticket_clicked,
       (SELECT count(*) FROM analytics_events e
         WHERE e.room_id = r.id AND e.event_name = 'match_shown') > 0 AS match_shown,
       r.state
FROM rooms r
LEFT JOIN analytics_room_activation a ON a.room_id = r.id
LEFT JOIN room_matches m ON m.room_id = r.id;

-- Daily TOP-10 dashboard measures. Filter by day in UTC or apply the desired
-- reporting timezone at query time. A zero denominator yields NULL by design.
CREATE VIEW analytics_daily_product_metrics AS
WITH dates AS (
    SELECT d::date AS metric_date FROM generate_series(
        (SELECT greatest(coalesce(min((created_at AT TIME ZONE 'UTC')::date), (now() AT TIME ZONE 'UTC')::date), (now() AT TIME ZONE 'UTC')::date - 89) FROM rooms),
        (now() AT TIME ZONE 'UTC')::date, interval '1 day') d
), room_daily AS (
    SELECT (created_at AT TIME ZONE 'UTC')::date AS metric_date,
           count(*) AS rooms_created,
           count(*) FILTER (WHERE invite_shared) AS rooms_invite_shared,
           count(*) FILTER (WHERE invite_opened) AS rooms_invite_opened,
           count(*) FILTER (WHERE first_joined_at IS NOT NULL) AS rooms_joined,
           count(*) FILTER (WHERE activated_at IS NOT NULL) AS activated_rooms,
           count(*) FILTER (WHERE matched_at IS NOT NULL) AS rooms_matched,
           count(*) FILTER (WHERE match_shown AND ticket_clicked) AS rooms_ticket_clicked,
           count(*) FILTER (WHERE match_shown) AS rooms_match_shown,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY matched_at - first_joined_at)
               FILTER (WHERE matched_at IS NOT NULL AND first_joined_at IS NOT NULL) AS median_time_to_match,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY first_joined_at - created_at)
               FILTER (WHERE first_joined_at IS NOT NULL) AS median_time_to_join,
           percentile_cont(0.75) WITHIN GROUP (ORDER BY first_joined_at - created_at)
               FILTER (WHERE first_joined_at IS NOT NULL) AS p75_time_to_join,
           percentile_cont(0.90) WITHIN GROUP (ORDER BY first_joined_at - created_at)
               FILTER (WHERE first_joined_at IS NOT NULL) AS p90_time_to_join,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY swipe_count)
               FILTER (WHERE matched_at IS NOT NULL) AS median_swipes_to_match
    FROM analytics_room_metrics
    GROUP BY (created_at AT TIME ZONE 'UTC')::date
), active_users AS (
    SELECT (occurred_at AT TIME ZONE 'UTC')::date AS metric_date, count(DISTINCT user_id) AS active_users
    FROM analytics_events
    GROUP BY (occurred_at AT TIME ZONE 'UTC')::date
), second_rooms AS (
    SELECT first.creator_user_id AS user_id, (first.created_at AT TIME ZONE 'UTC')::date AS metric_date,
           count(*) FILTER (WHERE second.created_at <= first.created_at + interval '7 days') > 0 AS second_7d,
           count(*) FILTER (WHERE second.created_at <= first.created_at + interval '30 days') > 0 AS second_30d
    FROM analytics_room_metrics first
    LEFT JOIN analytics_room_metrics second
      ON second.creator_user_id = first.creator_user_id AND second.created_at > first.created_at
    WHERE NOT EXISTS (
        SELECT 1 FROM analytics_room_metrics earlier
        WHERE earlier.creator_user_id = first.creator_user_id
          AND (earlier.created_at < first.created_at
               OR (earlier.created_at = first.created_at AND earlier.room_id < first.room_id))
    )
    GROUP BY first.creator_user_id, first.room_id, first.created_at
), retention AS (
    SELECT metric_date, count(*) FILTER (WHERE second_7d) AS users_second_room_7d,
           count(*) FILTER (WHERE second_30d) AS users_second_room_30d,
           count(*) AS users_first_room
    FROM second_rooms GROUP BY metric_date
)
SELECT d.metric_date,
       coalesce(a.active_users,0) AS active_users,
       coalesce(r.rooms_created,0) AS rooms_created,
       coalesce(r.rooms_invite_shared,0) AS rooms_invite_shared,
       coalesce(r.rooms_invite_opened,0) AS rooms_invite_opened,
       coalesce(r.rooms_joined,0) AS rooms_joined,
       coalesce(r.activated_rooms,0) AS activated_rooms,
       coalesce(r.rooms_matched,0) AS rooms_matched,
       coalesce(r.rooms_ticket_clicked,0) AS rooms_ticket_clicked,
           (SELECT count(DISTINCT rm.creator_user_id)::numeric
              FROM analytics_room_metrics rm WHERE (rm.created_at AT TIME ZONE 'UTC')::date = d.metric_date)
               / nullif(a.active_users,0) AS room_creation_rate,
       r.rooms_invite_shared::numeric / nullif(r.rooms_created,0) AS invite_share_rate,
       r.rooms_invite_opened::numeric / nullif(r.rooms_invite_shared,0) AS invite_open_rate,
       r.rooms_joined::numeric / nullif(r.rooms_invite_shared,0) AS invite_join_conversion,
       r.activated_rooms::numeric / nullif(r.rooms_created,0) AS room_activation_rate,
       r.rooms_matched::numeric / nullif(r.activated_rooms,0) AS match_rate,
       r.rooms_ticket_clicked::numeric / nullif(r.rooms_match_shown,0) AS match_ticket_ctr,
       r.median_time_to_match,
       r.median_time_to_join,
       r.p75_time_to_join,
       r.p90_time_to_join,
       r.median_swipes_to_match,
       t.users_second_room_7d::numeric / nullif(t.users_first_room,0) AS second_room_rate_7d,
       t.users_second_room_30d::numeric / nullif(t.users_first_room,0) AS second_room_rate_30d,
       (SELECT count(*) FILTER (WHERE event_name = 'event_liked')::numeric /
                   nullif(count(*) FILTER (WHERE event_name IN ('event_impression','event_swipe_impression')),0)
         FROM analytics_events e
         LEFT JOIN LATERAL (
             SELECT item.position
             FROM room_pools pool
             JOIN room_pool_events item ON item.pool_id = pool.id
             WHERE pool.room_id = e.room_id AND item.event_id = e.event_id
               AND pool.created_at <= e.occurred_at
             ORDER BY pool.created_at DESC
             LIMIT 1
         ) pool_item ON true
         WHERE (e.occurred_at AT TIME ZONE 'UTC')::date = d.metric_date
           AND e.room_id IS NOT NULL
           AND e.event_name IN ('event_liked','event_impression','event_swipe_impression')
           AND CASE WHEN coalesce(e.properties->>'recommendation_rank', e.properties->>'rank') ~ '^[0-9]+$'
                    THEN coalesce(e.properties->>'recommendation_rank', e.properties->>'rank')::integer <= 10
                    WHEN e.position IS NOT NULL THEN e.position < 10
                    WHEN pool_item.position IS NOT NULL THEN pool_item.position < 10
                    ELSE false END
       ) AS recommendation_top10_like_rate
FROM dates d
LEFT JOIN room_daily r USING (metric_date)
LEFT JOIN active_users a USING (metric_date)
LEFT JOIN retention t USING (metric_date);

CREATE VIEW analytics_recommendation_rank_metrics AS
WITH observations AS (
    SELECT e.*,
           coalesce(e.properties->>'recommendation_rank', e.properties->>'rank',
                    CASE WHEN e.position IS NOT NULL THEN (e.position + 1)::text END,
                    CASE WHEN pool_item.position IS NOT NULL THEN (pool_item.position + 1)::text END) AS observed_rank,
           coalesce(
               CASE WHEN e.properties->>'recommendation_score' ~ '^[0-9]+([.][0-9]+)?$'
                    THEN (e.properties->>'recommendation_score')::numeric END,
               pool_item.group_score::numeric) AS observed_score
    FROM analytics_events e
    LEFT JOIN LATERAL (
        SELECT item.position, item.group_score
        FROM room_pools pool
        JOIN room_pool_events item ON item.pool_id = pool.id
        WHERE pool.room_id = e.room_id AND item.event_id = e.event_id
          AND pool.created_at <= e.occurred_at
        ORDER BY pool.created_at DESC
        LIMIT 1
    ) pool_item ON true
)
SELECT observed_rank::integer AS recommendation_rank,
       count(*) FILTER (WHERE event_name IN ('event_impression','event_swipe_impression')) AS impressions,
       count(*) FILTER (WHERE event_name = 'event_liked') AS likes,
       count(*) FILTER (WHERE event_name = 'event_opened') AS opens,
       count(*) FILTER (WHERE event_name = 'event_saved') AS saves,
       count(*) FILTER (WHERE event_name = 'match_created') AS matches,
       count(*) FILTER (WHERE event_name = 'event_liked')::numeric /
           nullif(count(*) FILTER (WHERE event_name IN ('event_impression','event_swipe_impression')),0) AS like_rate,
       count(*) FILTER (WHERE event_name = 'event_opened')::numeric /
           nullif(count(*) FILTER (WHERE event_name IN ('event_impression','event_swipe_impression')),0) AS open_rate,
       count(*) FILTER (WHERE event_name = 'event_saved')::numeric /
           nullif(count(*) FILTER (WHERE event_name IN ('event_impression','event_swipe_impression')),0) AS save_rate,
       count(*) FILTER (WHERE event_name = 'match_created')::numeric /
           nullif(count(*) FILTER (WHERE event_name IN ('event_impression','event_swipe_impression')),0) AS match_rate,
       avg(observed_score) FILTER (WHERE event_name = 'event_liked') AS average_score_liked,
       avg(observed_score) FILTER (WHERE event_name = 'event_disliked') AS average_score_disliked
FROM observations
WHERE observed_rank ~ '^[0-9]+$' AND room_id IS NOT NULL
GROUP BY observed_rank::integer;

CREATE VIEW analytics_retention_cohorts AS
WITH user_activity AS (
    SELECT user_id, (occurred_at AT TIME ZONE 'UTC')::date AS activity_date
    FROM analytics_events
    GROUP BY user_id, (occurred_at AT TIME ZONE 'UTC')::date
), cohorts AS (
    SELECT user_id, min(activity_date) AS cohort_date
    FROM user_activity GROUP BY user_id
)
SELECT c.cohort_date,
       count(DISTINCT c.user_id) AS cohort_users,
       count(*) FILTER (WHERE a.activity_date = c.cohort_date + 1) AS retained_d1,
       count(*) FILTER (WHERE a.activity_date = c.cohort_date + 7) AS retained_d7,
       count(*) FILTER (WHERE a.activity_date = c.cohort_date + 14) AS retained_d14,
       count(*) FILTER (WHERE a.activity_date = c.cohort_date + 30) AS retained_d30,
       count(DISTINCT a.user_id) FILTER (WHERE a.activity_date = c.cohort_date + 1)::numeric /
           nullif(count(DISTINCT c.user_id),0) AS d1_retention,
       count(DISTINCT a.user_id) FILTER (WHERE a.activity_date = c.cohort_date + 7)::numeric /
           nullif(count(DISTINCT c.user_id),0) AS d7_retention,
       count(DISTINCT a.user_id) FILTER (WHERE a.activity_date = c.cohort_date + 14)::numeric /
           nullif(count(DISTINCT c.user_id),0) AS d14_retention,
       count(DISTINCT a.user_id) FILTER (WHERE a.activity_date = c.cohort_date + 30)::numeric /
           nullif(count(DISTINCT c.user_id),0) AS d30_retention
FROM cohorts c
LEFT JOIN user_activity a ON a.user_id = c.user_id
GROUP BY c.cohort_date;

CREATE VIEW analytics_guardrails AS
SELECT (SELECT count(*) FILTER (WHERE candidate_count = 0)::numeric /
               nullif(count(*),0) FROM room_pools) AS empty_pool_rate,
       (SELECT count(*) FILTER (WHERE event_name = 'room_creation_failed')::numeric /
               nullif(count(*) FILTER (WHERE event_name = 'room_creation_started'),0)
          FROM analytics_events) AS room_creation_failure_rate,
       (SELECT count(*) FILTER (WHERE event_name = 'room_join_failed')::numeric /
               nullif(count(*) FILTER (WHERE event_name = 'room_join_started'),0)
          FROM analytics_events) AS join_failure_rate,
       (SELECT count(*) FILTER (WHERE event_name = 'invite_share_failed')::numeric /
               nullif(count(*) FILTER (WHERE event_name = 'invite_shared'),0)
          FROM analytics_events) AS invite_share_failure_rate,
       (SELECT count(*) FILTER (WHERE event_name = 'ticket_redirect_failed')::numeric /
               nullif(count(*) FILTER (WHERE event_name = 'ticket_click'),0)
          FROM analytics_events) AS ticket_redirect_failure_rate,
       (SELECT count(*) FILTER (WHERE activated_at IS NOT NULL AND matched_at IS NULL)::numeric /
               nullif(count(*) FILTER (WHERE activated_at IS NOT NULL),0)
          FROM analytics_room_metrics) AS no_match_rate,
       (SELECT count(*) FILTER (WHERE state = 'exhausted' AND matched_event_id IS NULL)::numeric /
               nullif(count(*),0) FROM analytics_room_metrics) AS exhausted_without_match_rate,
       (SELECT count(*) FILTER (WHERE properties->>'status_code' ~ '^[0-9]+$'
                               AND (properties->>'status_code')::integer >= 400)::numeric /
               nullif(count(*),0)
          FROM analytics_events WHERE event_name = 'api_performance') AS api_error_rate,
       (SELECT count(*) FILTER (WHERE event_name = 'client_error')::numeric /
               nullif(count(*) FILTER (WHERE event_name IN ('client_error','client_performance')),0)
          FROM analytics_events) AS client_error_rate,
       (SELECT count(*) FILTER (WHERE state = 'failed')::numeric /
               nullif(count(*),0) FROM provider_sync_runs) AS external_provider_error_rate,
       (SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY
                   CASE WHEN properties->>'duration_ms' ~ '^[0-9]+([.][0-9]+)?$'
                        THEN (properties->>'duration_ms')::numeric END)
          FROM analytics_events
         WHERE properties->>'duration_ms' ~ '^[0-9]+([.][0-9]+)?$'
           AND (event_name = 'api_performance' AND coalesce(properties->>'critical','false') = 'true'
                OR event_name = 'client_performance' AND properties->>'operation' IN ('app_boot','feed_load','event_detail_load'))) AS critical_latency_p95_ms,
       (SELECT count(*) FILTER (WHERE NOT EXISTS (
                   SELECT 1 FROM event_images ei WHERE ei.event_id = e.id AND ei.role IN ('card','hero'))
               )::numeric / nullif(count(*),0)
          FROM events e WHERE e.is_demo = false AND e.status = 'published') AS events_without_image_rate,
       (SELECT count(*) FILTER (WHERE price_from_minor IS NULL)::numeric /
               nullif(count(*),0) FROM events
         WHERE is_demo = false AND status = 'published') AS events_without_price_rate,
       (SELECT count(*) FILTER (WHERE v.latitude IS NULL OR v.longitude IS NULL)::numeric /
               nullif(count(*),0)
          FROM events e JOIN venues v ON v.id = e.venue_id
         WHERE e.is_demo = false AND e.status = 'published') AS events_without_coordinates_rate;

CREATE VIEW analytics_provider_sync_metrics AS
SELECT provider, city_id, (started_at AT TIME ZONE 'UTC')::date AS metric_day,
       count(*) AS sync_runs,
       count(*) FILTER (WHERE state = 'succeeded') AS successful_runs,
       count(*) FILTER (WHERE state = 'failed') AS failed_runs,
       sum(errors) AS errors,
       sum(fetched) AS fetched,
       sum(inserted) AS inserted,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY completed_at - started_at)
         FILTER (WHERE completed_at IS NOT NULL) AS p95_duration
FROM provider_sync_runs
GROUP BY provider, city_id, (started_at AT TIME ZONE 'UTC')::date;

CREATE VIEW analytics_api_performance AS
WITH api_observations AS (
    SELECT (occurred_at AT TIME ZONE 'UTC')::date AS metric_day,
           coalesce(properties->>'operation', 'unknown') AS operation,
           event_name,
           CASE WHEN properties->>'status_code' ~ '^[0-9]+$'
                THEN (properties->>'status_code')::integer END AS status_code,
           CASE WHEN properties->>'duration_ms' ~ '^[0-9]+([.][0-9]+)?$'
                THEN (properties->>'duration_ms')::numeric END AS duration_ms
    FROM analytics_events
    WHERE event_name = 'api_performance'
)
SELECT metric_day, operation,
       count(*) AS requests_and_errors,
       count(*) FILTER (WHERE status_code < 400) AS successful_operations,
       count(*) FILTER (WHERE status_code >= 400) AS errors,
       count(*) FILTER (WHERE status_code >= 400)::numeric / nullif(count(*),0) AS error_rate,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms)
           FILTER (WHERE duration_ms IS NOT NULL) AS p50_duration_ms,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms)
           FILTER (WHERE duration_ms IS NOT NULL) AS p95_duration_ms,
       percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms)
           FILTER (WHERE duration_ms IS NOT NULL) AS p99_duration_ms
FROM api_observations
GROUP BY metric_day, operation;

CREATE VIEW analytics_pool_diversity AS
SELECT pool.id AS pool_id, pool.room_id, pool.version AS pool_version,
       count(DISTINCT category.category_slug) FILTER (WHERE item.position < 10) AS unique_categories_in_top_10,
       count(DISTINCT event.venue_id) FILTER (WHERE item.position < 10) AS unique_venues_in_top_10,
       count(DISTINCT event.source) FILTER (WHERE item.position < 20) AS unique_providers_in_top_20
FROM room_pools pool
JOIN room_pool_events item ON item.pool_id = pool.id
JOIN events event ON event.id = item.event_id
LEFT JOIN LATERAL (
    SELECT ec.category_slug FROM event_categories ec
    WHERE ec.event_id = event.id
    ORDER BY ec.is_primary DESC, ec.weight DESC, ec.category_slug
    LIMIT 1
) category ON true
GROUP BY pool.id, pool.room_id, pool.version;

CREATE VIEW analytics_repeat_exposure AS
WITH impressions AS (
    SELECT user_id, event_id, occurred_at, id,
           row_number() OVER (PARTITION BY user_id, event_id ORDER BY occurred_at, id) AS exposure_number
    FROM analytics_events
    WHERE event_name IN ('event_impression','event_swipe_impression') AND event_id IS NOT NULL
)
SELECT (occurred_at AT TIME ZONE 'UTC')::date AS metric_date,
       count(*) AS impressions,
       count(*) FILTER (WHERE exposure_number > 1) AS repeat_impressions,
       count(*) FILTER (WHERE exposure_number > 1)::numeric / nullif(count(*),0) AS repeat_event_exposure_rate
FROM impressions
GROUP BY (occurred_at AT TIME ZONE 'UTC')::date;

-- Catalog quality by actual provider, primary (or deterministic fallback)
-- category, event start day, and city. Coordinate completeness is structurally
-- guaranteed by NOT NULL today, but retained as an explicit observable field.
CREATE VIEW analytics_catalog_quality AS
WITH catalog AS (
    SELECT e.id, e.source AS provider, v.city_id, (e.starts_at AT TIME ZONE 'UTC')::date AS event_day,
           coalesce(category.category_slug, 'uncategorized') AS category,
           e.price_from_minor, e.ticket_available, e.ticket_url,
           e.description, v.latitude, v.longitude,
           EXISTS (SELECT 1 FROM event_images image
                   WHERE image.event_id = e.id AND image.role IN ('card','hero')) AS has_image
    FROM events e
    JOIN venues v ON v.id = e.venue_id
    LEFT JOIN LATERAL (
        SELECT ec.category_slug
        FROM event_categories ec
        WHERE ec.event_id = e.id
        ORDER BY ec.is_primary DESC, ec.category_slug
        LIMIT 1
    ) category ON true
    WHERE e.is_demo = false AND e.status = 'published'
)
SELECT provider, category, event_day, city_id,
       count(*) AS event_count,
       count(*) FILTER (WHERE has_image) AS events_with_image,
       count(*) FILTER (WHERE price_from_minor IS NOT NULL) AS events_with_price,
       count(*) FILTER (WHERE ticket_available AND ticket_url IS NOT NULL) AS events_with_ticket_link,
       count(*) FILTER (WHERE nullif(btrim(description), '') IS NOT NULL) AS events_with_description,
       count(*) FILTER (WHERE latitude IS NOT NULL AND longitude IS NOT NULL) AS events_with_coordinates,
       count(*) FILTER (WHERE NOT has_image)::numeric / nullif(count(*),0) AS missing_image_rate,
       count(*) FILTER (WHERE price_from_minor IS NULL)::numeric / nullif(count(*),0) AS missing_price_rate,
       count(*) FILTER (WHERE NOT ticket_available OR ticket_url IS NULL)::numeric /
           nullif(count(*),0) AS missing_ticket_link_rate,
       count(*) FILTER (WHERE nullif(btrim(description), '') IS NULL)::numeric /
           nullif(count(*),0) AS missing_description_rate,
       count(*) FILTER (WHERE latitude IS NULL OR longitude IS NULL)::numeric /
           nullif(count(*),0) AS missing_coordinates_rate
FROM catalog
GROUP BY provider, category, event_day, city_id;

-- Pairwise vote agreement. A pair is scoped to one pool and event, so repeated
-- rounds are not collapsed into a single vote fact. Category and price buckets
-- come directly from the catalog; rank comes from the persisted pool position.
CREATE VIEW analytics_vote_agreement AS
WITH vote_pairs AS (
    SELECT left_vote.room_id,
           (greatest(left_vote.created_at, right_vote.created_at) AT TIME ZONE 'UTC')::date AS vote_day,
           left_vote.vote AS left_vote,
           right_vote.vote AS right_vote,
           coalesce(event_category.category_slug, 'uncategorized') AS category,
           event.currency,
           CASE
               WHEN event.price_from_minor IS NULL THEN 'unknown'
               WHEN event.price_from_minor = 0 THEN 'free'
               WHEN event.price_from_minor < 300000 THEN 'under_3000'
               WHEN event.price_from_minor < 750000 THEN '3000_to_7499'
               WHEN event.price_from_minor < 1500000 THEN '7500_to_14999'
               ELSE '15000_plus'
           END AS price_bucket,
           pool_event.position AS recommendation_rank
    FROM room_votes left_vote
    JOIN room_votes right_vote
      ON right_vote.pool_id = left_vote.pool_id
     AND right_vote.event_id = left_vote.event_id
     AND right_vote.user_id > left_vote.user_id
    JOIN events event ON event.id = left_vote.event_id
    LEFT JOIN LATERAL (
        SELECT ec.category_slug
        FROM event_categories ec
        WHERE ec.event_id = event.id
        ORDER BY ec.is_primary DESC, ec.category_slug
        LIMIT 1
    ) event_category ON true
    JOIN room_pool_events pool_event
      ON pool_event.pool_id = left_vote.pool_id AND pool_event.event_id = left_vote.event_id
)
SELECT vote_day, category, currency, price_bucket, recommendation_rank,
       count(*) AS events_voted_by_both,
       count(*) FILTER (WHERE left_vote = right_vote) AS events_with_same_vote,
       count(*) FILTER (WHERE left_vote = 'like' AND right_vote = 'like') AS both_like,
       count(*) FILTER (WHERE left_vote = 'dislike' AND right_vote = 'dislike') AS both_dislike,
       count(*) FILTER (WHERE left_vote = 'like' AND right_vote = 'dislike') AS left_like_right_dislike,
       count(*) FILTER (WHERE left_vote = 'dislike' AND right_vote = 'like') AS left_dislike_right_like,
       count(*) FILTER (WHERE left_vote = right_vote)::numeric / nullif(count(*),0) AS agreement_rate,
       count(*) FILTER (WHERE left_vote = 'like' AND right_vote = 'like')::numeric /
           nullif(count(*),0) AS positive_agreement_rate
FROM vote_pairs
GROUP BY vote_day, category, currency, price_bucket, recommendation_rank;

-- +goose Down
DROP VIEW analytics_vote_agreement;
DROP VIEW analytics_catalog_quality;
DROP VIEW analytics_repeat_exposure;
DROP VIEW analytics_pool_diversity;
DROP VIEW analytics_api_performance;
DROP VIEW analytics_provider_sync_metrics;
DROP VIEW analytics_guardrails;
DROP VIEW analytics_retention_cohorts;
DROP VIEW analytics_recommendation_rank_metrics;
DROP VIEW analytics_daily_product_metrics;
DROP VIEW analytics_room_metrics;
DROP VIEW analytics_room_activation;
DROP VIEW analytics_events;
DROP FUNCTION analytics_prune_behavior_events();
DROP INDEX behavior_events_room_time_idx;
DROP INDEX behavior_events_type_time_idx;
DROP INDEX behavior_events_occurred_at_idx;
DROP INDEX behavior_events_session_idx;
DROP INDEX behavior_deduplication_key_idx;
ALTER TABLE behavior_events
    DROP COLUMN deduplication_key,
    DROP COLUMN properties,
    DROP COLUMN entry_point,
    DROP COLUMN app_version,
    DROP COLUMN platform,
    DROP COLUMN session_id,
    DROP COLUMN event_version;
ALTER TABLE behavior_events DROP CONSTRAINT behavior_events_check;
ALTER TABLE behavior_events ADD CONSTRAINT behavior_events_check CHECK (
    origin = 'server' OR type IN ('impression','open','share')
);
