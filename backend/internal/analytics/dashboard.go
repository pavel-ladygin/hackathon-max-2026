// Package analytics serves the private, aggregate-only product analytics dashboard.
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

const (
	queryTimeout = 20 * time.Second
	cacheTTL     = 30 * time.Second
)

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type cacheEntry struct {
	body    []byte
	expires time.Time
}
type Handler struct {
	db    queryer
	now   func() time.Time
	mu    sync.Mutex
	cache map[int]cacheEntry
}

func NewHandler(db queryer) *Handler {
	return &Handler{db: db, now: time.Now, cache: make(map[int]cacheEntry, 3)}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/internal/analytics/dashboard", h.GetDashboard)
}

func (h *Handler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || (parsed != 7 && parsed != 30 && parsed != 90) {
			httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: http.StatusBadRequest, Code: "VALIDATION_FAILED", Message: "days must be 7, 30, or 90"})
			return
		}
		days = parsed
	}
	now := h.now()
	h.mu.Lock()
	if entry, ok := h.cache[days]; ok && now.Before(entry.expires) {
		body := append([]byte(nil), entry.body...)
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	var payload []byte
	if err := h.db.QueryRow(ctx, dashboardQuery, days).Scan(&payload); err != nil {
		slog.Error("analytics dashboard query failed", "request_id", httpapi.RequestID(r.Context()), "period_days", days, "error", err)
		status := http.StatusInternalServerError
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: "INTERNAL", Message: "Dashboard data unavailable"})
		return
	}
	if !json.Valid(payload) {
		httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "Dashboard data unavailable"})
		return
	}
	h.mu.Lock()
	// The only cache keys are the three validated periods, so memory use is fixed.
	h.cache[days] = cacheEntry{body: append([]byte(nil), payload...), expires: now.Add(cacheTTL)}
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

const dashboardQuery = `
WITH daily AS (
 SELECT metric_date AS date, active_users, rooms_created, rooms_invite_shared, rooms_invite_opened,
        rooms_joined, activated_rooms, rooms_matched, rooms_ticket_clicked,
        room_creation_rate, invite_share_rate, invite_open_rate, invite_join_conversion,
        room_activation_rate, match_rate, match_ticket_ctr,
        extract(epoch FROM median_time_to_match)::double precision AS median_time_to_match_seconds,
        extract(epoch FROM median_time_to_join)::double precision AS median_time_to_join_seconds,
        extract(epoch FROM p75_time_to_join)::double precision AS p75_time_to_join_seconds,
        extract(epoch FROM p90_time_to_join)::double precision AS p90_time_to_join_seconds,
        median_swipes_to_match, second_room_rate_7d, second_room_rate_30d, recommendation_top10_like_rate,
        (SELECT count(*) FROM analytics_room_metrics rm
          WHERE (rm.created_at AT TIME ZONE 'UTC')::date = metric_date AND rm.match_shown) AS rooms_match_shown
 FROM analytics_daily_product_metrics
 WHERE metric_date >= (now() AT TIME ZONE 'UTC')::date - ($1::int - 1)
 ORDER BY metric_date
), room_period AS (
 SELECT extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY matched_at - first_joined_at)
          FILTER (WHERE matched_at IS NOT NULL AND first_joined_at IS NOT NULL))::double precision median_time_to_match_seconds,
        percentile_cont(0.5) WITHIN GROUP (ORDER BY swipe_count)
          FILTER (WHERE matched_at IS NOT NULL)::double precision median_swipes_to_match,
        extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY first_joined_at - created_at)
          FILTER (WHERE first_joined_at IS NOT NULL))::double precision median_time_to_join_seconds
 FROM analytics_room_metrics
 WHERE created_at >= ((now() AT TIME ZONE 'UTC')::date - ($1::int - 1))::timestamp AT TIME ZONE 'UTC'
), second_room_period AS (
 SELECT count(*) FILTER (WHERE cohort.created_at <= now() - interval '7 days' AND has_second_7d)::numeric /
          nullif(count(*) FILTER (WHERE cohort.created_at <= now() - interval '7 days'), 0) second_room_rate_7d,
        count(*) FILTER (WHERE cohort.created_at <= now() - interval '30 days' AND has_second_30d)::numeric /
          nullif(count(*) FILTER (WHERE cohort.created_at <= now() - interval '30 days'), 0) second_room_rate_30d
 FROM (
   SELECT first_room.created_at,
          EXISTS (SELECT 1 FROM analytics_room_metrics second
                  WHERE second.creator_user_id = first_room.creator_user_id AND second.created_at > first_room.created_at
                    AND second.created_at <= first_room.created_at + interval '7 days') has_second_7d,
          EXISTS (SELECT 1 FROM analytics_room_metrics second
                  WHERE second.creator_user_id = first_room.creator_user_id AND second.created_at > first_room.created_at
                    AND second.created_at <= first_room.created_at + interval '30 days') has_second_30d
   FROM analytics_room_metrics first_room
   WHERE first_room.created_at >= ((now() AT TIME ZONE 'UTC')::date - ($1::int - 1))::timestamp AT TIME ZONE 'UTC'
     AND NOT EXISTS (SELECT 1 FROM analytics_room_metrics earlier
                     WHERE earlier.creator_user_id = first_room.creator_user_id
                       AND (earlier.created_at < first_room.created_at
                            OR (earlier.created_at = first_room.created_at AND earlier.room_id < first_room.room_id)))
 ) cohort
), daily_summary AS (
 SELECT (SELECT count(DISTINCT user_id) FROM analytics_events
          WHERE occurred_at >= ((now() AT TIME ZONE 'UTC')::date - ($1::int - 1))::timestamp AT TIME ZONE 'UTC') active_users,
        coalesce(sum(rooms_created),0) rooms_created,
        coalesce(sum(rooms_invite_shared),0) rooms_invite_shared, coalesce(sum(rooms_invite_opened),0) rooms_invite_opened,
        coalesce(sum(rooms_joined),0) rooms_joined, coalesce(sum(activated_rooms),0) activated_rooms,
        coalesce(sum(rooms_matched),0) rooms_matched, coalesce(sum(rooms_ticket_clicked),0) rooms_ticket_clicked,
        sum(rooms_invite_shared)::numeric / nullif(sum(rooms_created),0) invite_share_rate,
        (SELECT count(DISTINCT creator_user_id)::numeric / nullif((SELECT count(DISTINCT user_id) FROM analytics_events
          WHERE occurred_at >= ((now() AT TIME ZONE 'UTC')::date - ($1::int - 1))::timestamp AT TIME ZONE 'UTC'),0)
           FROM analytics_room_metrics
          WHERE created_at >= ((now() AT TIME ZONE 'UTC')::date - ($1::int - 1))::timestamp AT TIME ZONE 'UTC') room_creation_rate,
        sum(rooms_invite_opened)::numeric / nullif(sum(rooms_invite_shared),0) invite_open_rate,
        sum(rooms_joined)::numeric / nullif(sum(rooms_invite_shared),0) invite_join_conversion,
        sum(activated_rooms)::numeric / nullif(sum(rooms_created),0) room_activation_rate,
        sum(rooms_matched)::numeric / nullif(sum(activated_rooms),0) match_rate,
        (SELECT count(*) FILTER (WHERE match_shown AND ticket_clicked)::numeric /
                nullif(count(*) FILTER (WHERE match_shown),0)
           FROM analytics_room_metrics
          WHERE created_at >= ((now() AT TIME ZONE 'UTC')::date - ($1::int - 1))::timestamp AT TIME ZONE 'UTC') match_ticket_ctr,
        (SELECT median_time_to_match_seconds FROM room_period) median_time_to_match_seconds,
        (SELECT median_swipes_to_match FROM room_period) median_swipes_to_match,
        (SELECT median_time_to_join_seconds FROM room_period) median_time_to_join_seconds,
        (SELECT second_room_rate_7d FROM second_room_period) second_room_rate_7d,
        (SELECT second_room_rate_30d FROM second_room_period) second_room_rate_30d
 FROM analytics_daily_product_metrics
 WHERE metric_date >= (now() AT TIME ZONE 'UTC')::date - ($1::int - 1)
), rank_metrics AS (
 SELECT recommendation_rank AS rank, impressions, likes, opens, saves, matches, like_rate, open_rate, save_rate,
        match_rate, average_score_liked, average_score_disliked
 FROM analytics_recommendation_rank_metrics WHERE recommendation_rank BETWEEN 1 AND 100
 ORDER BY recommendation_rank
), retention AS (
 SELECT cohort_date, cohort_users,
        CASE WHEN cohort_date + 1 <= (now() AT TIME ZONE 'UTC')::date THEN d1_retention END d1_retention,
        CASE WHEN cohort_date + 7 <= (now() AT TIME ZONE 'UTC')::date THEN d7_retention END d7_retention,
        CASE WHEN cohort_date + 14 <= (now() AT TIME ZONE 'UTC')::date THEN d14_retention END d14_retention,
        CASE WHEN cohort_date + 30 <= (now() AT TIME ZONE 'UTC')::date THEN d30_retention END d30_retention
 FROM analytics_retention_cohorts
 WHERE cohort_date >= (now() AT TIME ZONE 'UTC')::date - ($1::int - 1)
 ORDER BY cohort_date
), providers AS (
 SELECT provider, metric_day AS date, sum(sync_runs)::bigint sync_runs,
        sum(successful_runs)::bigint successful_runs, sum(failed_runs)::bigint failed_runs,
        sum(errors)::bigint errors, sum(fetched)::bigint fetched, sum(inserted)::bigint inserted,
        max(extract(epoch FROM p95_duration))::double precision p95_duration_seconds
 FROM analytics_provider_sync_metrics
 WHERE metric_day >= (now() AT TIME ZONE 'UTC')::date - ($1::int - 1)
 GROUP BY provider, metric_day ORDER BY metric_day, provider LIMIT 500
), api_perf AS (
 SELECT metric_day AS date, operation, requests_and_errors, successful_operations, errors, error_rate,
        p50_duration_ms, p95_duration_ms, p99_duration_ms
 FROM analytics_api_performance
 WHERE metric_day >= (now() AT TIME ZONE 'UTC')::date - ($1::int - 1)
 ORDER BY metric_day, operation LIMIT 1000
), catalog AS (
 SELECT provider, category, event_day AS date, sum(event_count)::bigint event_count,
        sum(events_with_image)::bigint events_with_image, sum(events_with_price)::bigint events_with_price,
        sum(events_with_ticket_link)::bigint events_with_ticket_link,
        sum(events_with_description)::bigint events_with_description,
        sum(events_with_coordinates)::bigint events_with_coordinates,
        sum(events_with_image)::numeric/nullif(sum(event_count),0) image_rate,
        sum(events_with_price)::numeric/nullif(sum(event_count),0) price_rate,
        sum(events_with_ticket_link)::numeric/nullif(sum(event_count),0) ticket_link_rate,
        sum(events_with_description)::numeric/nullif(sum(event_count),0) description_rate,
        sum(events_with_coordinates)::numeric/nullif(sum(event_count),0) coordinates_rate
 FROM analytics_catalog_quality
 GROUP BY provider, category, event_day ORDER BY event_day DESC, event_count DESC LIMIT 500
), agreement AS (
 SELECT vote_day AS date, category, currency, price_bucket, recommendation_rank AS rank,
        sum(events_voted_by_both)::bigint events_voted_by_both,
        sum(events_with_same_vote)::bigint events_with_same_vote,
        sum(both_like)::bigint both_like, sum(both_dislike)::bigint both_dislike,
        sum(left_like_right_dislike)::bigint left_like_right_dislike,
        sum(left_dislike_right_like)::bigint left_dislike_right_like,
        sum(events_with_same_vote)::numeric/nullif(sum(events_voted_by_both),0) agreement_rate,
        sum(both_like)::numeric/nullif(sum(events_voted_by_both),0) positive_agreement_rate
 FROM analytics_vote_agreement
 WHERE vote_day >= (now() AT TIME ZONE 'UTC')::date - ($1::int - 1)
 GROUP BY vote_day, category, currency, price_bucket, recommendation_rank
 ORDER BY vote_day DESC, events_voted_by_both DESC LIMIT 500
), diversity AS (
 SELECT count(*)::bigint pools,
        avg(d.unique_categories_in_top_10)::double precision average_categories_top10,
        avg(d.unique_venues_in_top_10)::double precision average_venues_top10,
        avg(d.unique_providers_in_top_20)::double precision average_providers_top20
 FROM analytics_pool_diversity d
 WHERE d.pool_version = (SELECT max(p.version) FROM room_pools p WHERE p.room_id = d.room_id)
), repeats AS (
 SELECT metric_date AS date, impressions, repeat_impressions, repeat_event_exposure_rate
 FROM analytics_repeat_exposure
 WHERE metric_date >= (now() AT TIME ZONE 'UTC')::date - ($1::int - 1)
 ORDER BY metric_date
), agreement_summary AS (
 SELECT coalesce(sum(events_with_same_vote), 0)::bigint events_with_same_vote,
        coalesce(sum(events_voted_by_both), 0)::bigint events_voted_by_both,
        sum(events_with_same_vote)::numeric / nullif(sum(events_voted_by_both), 0) agreement_rate
 FROM analytics_vote_agreement
 WHERE vote_day >= (now() AT TIME ZONE 'UTC')::date - ($1::int - 1)
)
SELECT jsonb_build_object(
 'period_days', $1,
 'generated_at', now(),
 'daily', coalesce((SELECT jsonb_agg(to_jsonb(daily)) FROM daily), '[]'::jsonb),
 'summary', (SELECT to_jsonb(daily_summary) FROM daily_summary),
 'recommendations', coalesce((SELECT jsonb_agg(to_jsonb(rank_metrics)) FROM rank_metrics), '[]'::jsonb),
 'retention', coalesce((SELECT jsonb_agg(to_jsonb(retention)) FROM retention), '[]'::jsonb),
 'guardrails', (SELECT to_jsonb(analytics_guardrails) FROM analytics_guardrails),
 'providers', coalesce((SELECT jsonb_agg(to_jsonb(providers)) FROM providers), '[]'::jsonb),
 'api_performance', coalesce((SELECT jsonb_agg(to_jsonb(api_perf)) FROM api_perf), '[]'::jsonb),
 'catalog_quality', coalesce((SELECT jsonb_agg(to_jsonb(catalog)) FROM catalog), '[]'::jsonb),
 'vote_agreement', coalesce((SELECT jsonb_agg(to_jsonb(agreement)) FROM agreement), '[]'::jsonb),
 'vote_agreement_summary', (SELECT to_jsonb(agreement_summary) FROM agreement_summary),
 'pool_diversity', (SELECT to_jsonb(diversity) FROM diversity),
 'repeat_exposure', coalesce((SELECT jsonb_agg(to_jsonb(repeats)) FROM repeats), '[]'::jsonb)
)`
