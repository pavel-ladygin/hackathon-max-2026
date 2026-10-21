-- +goose Up
-- Additive analytics only. Existing analytics_* views and source events remain
-- unchanged. All facts describe the current transaction snapshot, not replay of
-- an arbitrary past snapshot. Windows use [start,end), timestamps are UTC.
-- Existing databases already above version 27 need an explicit missing-version
-- migration application by the integrator; never renumber applied migrations.

-- A generated round is a room/round pair, not a pool version. Ignore ranking
-- drafts; the latest committed ready/exhausted version represents the round.
-- Exhausted pool state survives restart even when rooms.state changes.
CREATE VIEW analytics_recommendation_rounds AS
SELECT DISTINCT ON (p.room_id, p.round_no)
       p.room_id, p.round_no, p.id AS pool_id, p.version AS pool_version,
       p.created_at AS generated_at, p.candidate_count, p.ranker_version,
       (p.candidate_count = 0) AS zero_pool,
       (p.candidate_count > 0 AND p.state = 'exhausted') AS nonempty_exhausted,
       (p.state = 'exhausted') AS exhausted,
       EXISTS (SELECT 1 FROM room_pool_events pe JOIN events e ON e.id = pe.event_id
               WHERE pe.pool_id = p.id AND (e.is_demo OR e.source IN ('test','synthetic'))) AS contains_test_events
FROM room_pools p
WHERE p.state IN ('ready','exhausted') AND p.created_at <= now()
ORDER BY p.room_id, p.round_no, p.version DESC;

-- Conservative attribution. Client properties claiming a recipient role alone
-- are not proof. A consumed invitation + matching noncreator membership proves
-- a historical pre-join preview. Unjoined client previews stay unverified.
-- Server preview context is trusted only when recorded with server origin.
CREATE VIEW analytics_recommendation_room_events AS
SELECT e.id, e.room_id, e.event_name, e.occurred_at,
       (e.event_name = 'invite_shared' AND e.user_id = r.creator_user_id
        AND (e.origin = 'server' OR EXISTS (
          SELECT 1 FROM room_members m WHERE m.room_id = r.id AND m.user_id = e.user_id
          AND m.role = 'creator' AND m.joined_at <= e.occurred_at))) AS share_attempt,
       (e.event_name IN ('invite_opened','invite_link_opened') AND e.user_id <> r.creator_user_id
        AND ((e.origin = 'server' AND e.properties->>'context' IN ('recipient_preview','pre_join'))
          OR EXISTS (
            SELECT 1 FROM room_invites i JOIN room_members m
              ON m.room_id = i.room_id AND m.user_id = i.consumed_by AND m.role = 'participant'
            WHERE i.room_id = r.id AND i.consumed_by = e.user_id
              AND i.created_at <= e.occurred_at AND i.expires_at > e.occurred_at
              AND i.consumed_at >= e.occurred_at AND i.consumed_at <= now()
              AND m.joined_at >= e.occurred_at AND m.joined_at <= now()
              AND e.received_at <= m.joined_at
          ))) AS recipient_preview,
       (e.event_name = 'match_opened' AND EXISTS (
          SELECT 1 FROM room_matches match JOIN room_members m ON m.room_id = match.room_id
          WHERE match.room_id = r.id AND match.event_id = e.event_id AND m.user_id = e.user_id
            AND m.joined_at <= e.occurred_at AND match.matched_at <= e.occurred_at
            AND m.joined_at <= e.received_at AND match.matched_at <= e.received_at
       )) AS match_event_open_click,
       (e.event_name = 'ticket_click' AND e.origin = 'server' AND EXISTS (
          SELECT 1 FROM room_matches match JOIN room_members m ON m.room_id = match.room_id
          WHERE match.room_id = r.id AND match.event_id = e.event_id AND m.user_id = e.user_id
            AND m.joined_at <= e.occurred_at AND match.matched_at <= e.occurred_at
            AND m.joined_at <= e.received_at AND match.matched_at <= e.received_at
       )) AS ticket_click
FROM analytics_events e JOIN rooms r ON r.id = e.room_id
WHERE e.occurred_at >= r.created_at AND e.occurred_at <= now() AND e.received_at <= now()
  AND e.event_name IN ('invite_shared','invite_opened','invite_link_opened','match_opened','ticket_click');

CREATE VIEW analytics_recommendation_rooms AS
WITH ready_rounds AS (
  SELECT s.room_id, s.round_no, max(s.ready_at) AS both_ready_at
  FROM room_member_round_state s JOIN room_members m USING (room_id,user_id)
  JOIN rooms r ON r.id = s.room_id
  WHERE s.ready AND s.ready_at <= now() AND m.joined_at <= s.ready_at
    AND ((m.role = 'creator' AND m.user_id = r.creator_user_id)
      OR (m.role = 'participant' AND m.user_id <> r.creator_user_id))
  GROUP BY s.room_id, s.round_no
  HAVING count(DISTINCT s.user_id) = 2 AND bool_or(m.role = 'creator') AND bool_or(m.role = 'participant')
), ready_facts AS (
  SELECT room_id, both_ready_at FROM ready_rounds
  UNION ALL
  -- A committed generated pool is a server fact requiring both ready intents,
  -- including zero-pool. It remains usable when old readiness rows are absent.
  SELECT room_id, generated_at FROM analytics_recommendation_rounds
), member_votes AS (
  SELECT v.room_id, v.user_id, min(v.created_at) AS first_vote_at
  FROM room_votes v JOIN room_members m USING (room_id,user_id)
  JOIN rooms r ON r.id = v.room_id
  WHERE v.created_at <= now() AND v.created_at >= m.joined_at
    AND ((m.role = 'creator' AND m.user_id = r.creator_user_id)
      OR (m.role = 'participant' AND m.user_id <> r.creator_user_id))
  GROUP BY v.room_id,v.user_id
), votes AS (
  SELECT room_id, max(first_vote_at) AS both_voted_at FROM member_votes
  GROUP BY room_id HAVING count(*) = 2
)
SELECT r.id AS room_id, r.created_at, r.expires_at,
       (SELECT min(m.joined_at) FROM room_members m WHERE m.room_id = r.id
         AND m.role = 'participant' AND m.user_id <> r.creator_user_id AND m.joined_at <= now()) AS second_user_joined_at,
       (SELECT min(both_ready_at) FROM ready_facts f WHERE f.room_id = r.id) AS both_intents_ready_at,
       v.both_voted_at AS both_distinct_users_voted_at,
       match.matched_at AS room_matched_at, mp.round_no AS matched_round,
       CASE WHEN match.room_id IS NOT NULL THEN 'matched'
            WHEN r.state = 'closed' THEN 'closed'
            WHEN r.state = 'exhausted' AND r.round_no = 3 AND EXISTS (
              SELECT 1 FROM analytics_recommendation_rounds p WHERE p.room_id = r.id AND p.round_no = 3 AND p.exhausted
            ) THEN 'completed_no_match'
            ELSE 'incomplete' END AS outcome,
       EXISTS (SELECT 1 FROM analytics_recommendation_room_events e WHERE e.room_id = r.id AND e.share_attempt) AS invite_share_attempted,
       EXISTS (SELECT 1 FROM analytics_recommendation_room_events e WHERE e.room_id = r.id AND e.recipient_preview) AS recipient_previewed,
       EXISTS (SELECT 1 FROM analytics_recommendation_room_events e WHERE e.room_id = r.id AND e.match_event_open_click) AS match_event_open_clicked,
       (SELECT count(*) FROM analytics_recommendation_room_events e WHERE e.room_id = r.id AND e.match_event_open_click) AS match_event_open_clicks,
       EXISTS (SELECT 1 FROM analytics_recommendation_room_events e WHERE e.room_id = r.id AND e.ticket_click) AS ticket_clicked,
       (SELECT count(*) FROM analytics_recommendation_room_events e WHERE e.room_id = r.id AND e.ticket_click) AS ticket_clicks,
       (mp.round_no IN (2,3) AND EXISTS (
         SELECT 1 FROM analytics_recommendation_rounds p WHERE p.room_id = r.id AND p.round_no < mp.round_no AND p.exhausted
       )) AS recovered_match,
       EXISTS (SELECT 1 FROM analytics_recommendation_rounds p WHERE p.room_id = r.id AND p.contains_test_events) AS contains_test_events
FROM rooms r LEFT JOIN votes v ON v.room_id = r.id
LEFT JOIN room_matches match ON match.room_id = r.id AND match.matched_at <= now()
LEFT JOIN room_pools mp ON mp.id = match.pool_id
WHERE r.created_at <= now();

-- +goose Down
DROP VIEW analytics_recommendation_rooms;
DROP VIEW analytics_recommendation_room_events;
DROP VIEW analytics_recommendation_rounds;
