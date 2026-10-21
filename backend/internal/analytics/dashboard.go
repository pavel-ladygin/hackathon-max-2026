// Package analytics serves the private, aggregate-only product analytics dashboard.
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
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
	return &Handler{db: db, now: time.Now, cache: make(map[int]cacheEntry, 6)}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/internal/analytics/dashboard", h.GetDashboard)
	r.Get("/api/v1/internal/analytics/recommendations", h.GetRecommendationAnalytics)
}

func (h *Handler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	h.getDashboard(w, r, dashboardQuery, 0)
}

// GetRecommendationAnalytics exports versioned aggregate KPIs and their N/D
// definitions. It uses the same internal-admin boundary as the legacy dashboard.
func (h *Handler) GetRecommendationAnalytics(w http.ResponseWriter, r *http.Request) {
	h.getDashboard(w, r, recommendationAnalyticsQuery, 100)
}

func (h *Handler) getDashboard(w http.ResponseWriter, r *http.Request, query string, cacheOffset int) {
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
	key := days + cacheOffset
	h.mu.Lock()
	if entry, ok := h.cache[key]; ok && now.Before(entry.expires) {
		body := append([]byte(nil), entry.body...)
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		setAnalyticsDownload(w, r, cacheOffset)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	var payload []byte
	var err error
	if cacheOffset == 100 {
		payload, err = recommendationPayload(ctx, h.db, days)
	} else {
		err = h.db.QueryRow(ctx, query, days).Scan(&payload)
	}
	if err != nil {
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
	// Three validated periods per version, with disjoint cache keys.
	h.cache[key] = cacheEntry{body: append([]byte(nil), payload...), expires: now.Add(cacheTTL)}
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setAnalyticsDownload(w, r, cacheOffset)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func setAnalyticsDownload(w http.ResponseWriter, r *http.Request, cacheOffset int) {
	if cacheOffset == 100 && r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="recommendation-kpis-v2.json"`)
	}
}

// Recommendation facts come from one SQL snapshot. HTTP response structure,
// ratio calculation and descriptive metadata belong to the application.
type recommendationRoomCounts struct {
	RoomCreated                 int64 `json:"room_created"`
	SecondUserJoined            int64 `json:"second_user_joined"`
	BothIntentsReady            int64 `json:"both_intents_ready"`
	BothDistinctUsersVoted      int64 `json:"both_distinct_users_voted"`
	RoomMatched                 int64 `json:"room_matched"`
	CompletedNoMatch            int64 `json:"completed_no_match"`
	Incomplete                  int64 `json:"incomplete"`
	ExpiredIncomplete           int64 `json:"expired_incomplete"`
	Closed                      int64 `json:"closed"`
	FirstRoundMatch             int64 `json:"first_round_match"`
	RecoveredRound2             int64 `json:"recovered_round_2"`
	RecoveredRound3             int64 `json:"recovered_round_3"`
	InviteShareAttemptRooms     int64 `json:"invite_share_attempt_rooms"`
	RecipientPreviewRooms       int64 `json:"recipient_preview_rooms"`
	SharedRecipientPreviewRooms int64 `json:"shared_recipient_preview_rooms"`
	MatchEventOpenClickRooms    int64 `json:"match_event_open_click_rooms"`
	MatchEventOpenClicks        int64 `json:"match_event_open_clicks"`
	TicketClickRooms            int64 `json:"ticket_click_rooms"`
	TicketClicks                int64 `json:"ticket_clicks"`
	RoomsWithTestEvents         int64 `json:"rooms_with_test_events"`
}

type recommendationRoundCounts struct {
	GeneratedRounds         int64 `json:"generated_rounds"`
	ZeroPoolRounds          int64 `json:"zero_pool_rounds"`
	NonemptyRoundsGenerated int64 `json:"nonempty_rounds_generated"`
	NonemptyRoundsExhausted int64 `json:"nonempty_rounds_exhausted"`
}

type recommendationFacts struct {
	SnapshotAt  time.Time
	PeriodStart time.Time
	PeriodEnd   time.Time
	RoomCounts  recommendationRoomCounts
	RoundCounts recommendationRoundCounts
}

type recommendationRatio struct {
	Numerator   int64    `json:"numerator"`
	Denominator int64    `json:"denominator"`
	Rate        *float64 `json:"rate"`
}

type recommendationLegacyBaseline struct {
	Cohort string   `json:"cohort"`
	Ratios []string `json:"ratios"`
	Source string   `json:"source"`
}

type recommendationUnverifiedClaim struct {
	Claim  string `json:"claim"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type recommendationExport struct {
	MetricsVersion   string                          `json:"metrics_version"`
	ReleaseVersion   string                          `json:"release_version"`
	ReleaseRevision  string                          `json:"release_revision"`
	SnapshotAt       time.Time                       `json:"snapshot_at"`
	PeriodStart      time.Time                       `json:"period_start"`
	PeriodEnd        time.Time                       `json:"period_end"`
	Timezone         string                          `json:"timezone"`
	RoomCounts       recommendationRoomCounts        `json:"room_counts"`
	RoundCounts      recommendationRoundCounts       `json:"round_counts"`
	Rates            map[string]recommendationRatio  `json:"rates"`
	RateDefinitions  map[string]string               `json:"rate_definitions"`
	Definitions      map[string]string               `json:"definitions"`
	LegacyBaseline   recommendationLegacyBaseline    `json:"legacy_baseline"`
	UnverifiedClaims []recommendationUnverifiedClaim `json:"unverified_claims"`
}

func recommendationRate(n, d int64) recommendationRatio {
	result := recommendationRatio{Numerator: n, Denominator: d}
	if d != 0 {
		rate := float64(n) / float64(d)
		result.Rate = &rate
	}
	return result
}

func recommendationPayload(ctx context.Context, db queryer, days int) ([]byte, error) {
	var facts recommendationFacts
	rooms, rounds := &facts.RoomCounts, &facts.RoundCounts
	if err := db.QueryRow(ctx, recommendationAnalyticsQuery, days).Scan(
		&facts.SnapshotAt, &facts.PeriodStart, &facts.PeriodEnd,
		&rooms.RoomCreated,
		&rooms.SecondUserJoined,
		&rooms.BothIntentsReady,
		&rooms.BothDistinctUsersVoted,
		&rooms.RoomMatched,
		&rooms.CompletedNoMatch,
		&rooms.Incomplete,
		&rooms.ExpiredIncomplete,
		&rooms.Closed,
		&rooms.FirstRoundMatch,
		&rooms.RecoveredRound2,
		&rooms.RecoveredRound3,
		&rooms.InviteShareAttemptRooms,
		&rooms.RecipientPreviewRooms,
		&rooms.SharedRecipientPreviewRooms,
		&rooms.MatchEventOpenClickRooms,
		&rooms.MatchEventOpenClicks,
		&rooms.TicketClickRooms,
		&rooms.TicketClicks,
		&rooms.RoomsWithTestEvents,
		&rounds.GeneratedRounds,
		&rounds.ZeroPoolRounds,
		&rounds.NonemptyRoundsGenerated,
		&rounds.NonemptyRoundsExhausted,
	); err != nil {
		return nil, err
	}
	return json.Marshal(newRecommendationExport(facts))
}

func newRecommendationExport(facts recommendationFacts) recommendationExport {
	r, p := facts.RoomCounts, facts.RoundCounts
	return recommendationExport{
		MetricsVersion:  "recommendation-analytics-v2",
		ReleaseVersion:  "recommendation-analytics-v2/000027",
		ReleaseRevision: analyticsBuildRevision(),
		SnapshotAt:      facts.SnapshotAt.UTC(), PeriodStart: facts.PeriodStart.UTC(), PeriodEnd: facts.PeriodEnd.UTC(), Timezone: "UTC",
		RoomCounts: r, RoundCounts: p,
		Rates: map[string]recommendationRatio{
			"room_join":                     recommendationRate(r.SecondUserJoined, r.RoomCreated),
			"room_activation":               recommendationRate(r.BothIntentsReady, r.RoomCreated),
			"both_users_voted":              recommendationRate(r.BothDistinctUsersVoted, r.RoomCreated),
			"created_to_match":              recommendationRate(r.RoomMatched, r.RoomCreated),
			"activated_to_match":            recommendationRate(r.RoomMatched, r.BothIntentsReady),
			"completed_no_match":            recommendationRate(r.CompletedNoMatch, r.RoomMatched+r.CompletedNoMatch),
			"zero_pool_rounds":              recommendationRate(p.ZeroPoolRounds, p.GeneratedRounds),
			"nonempty_rounds_exhausted":     recommendationRate(p.NonemptyRoundsExhausted, p.NonemptyRoundsGenerated),
			"recipient_preview_after_share": recommendationRate(r.SharedRecipientPreviewRooms, r.InviteShareAttemptRooms),
			"match_event_open_click":        recommendationRate(r.MatchEventOpenClickRooms, r.RoomMatched),
			"ticket_click":                  recommendationRate(r.TicketClickRooms, r.RoomMatched),
		},
		RateDefinitions: map[string]string{
			"room_join":                     "rooms: second_user_joined / room_created",
			"room_activation":               "rooms: both_intents_ready / room_created",
			"both_users_voted":              "rooms: both_distinct_users_voted / room_created",
			"created_to_match":              "rooms: room_matched / room_created",
			"activated_to_match":            "rooms: room_matched / both_intents_ready",
			"completed_no_match":            "rooms: completed_no_match / (room_matched + completed_no_match); excludes incomplete and closed",
			"zero_pool_rounds":              "rounds: zero_pool_rounds / generated_rounds",
			"nonempty_rounds_exhausted":     "rounds: nonempty_rounds_exhausted / nonempty_rounds_generated",
			"recipient_preview_after_share": "rooms: shared_recipient_preview_rooms / invite_share_attempt_rooms",
			"match_event_open_click":        "rooms: match_event_open_click_rooms / room_matched",
			"ticket_click":                  "rooms: ticket_click_rooms / room_matched",
		},
		Definitions: map[string]string{
			"room_cohort":        "room.created_at in [period_start,period_end); progress observed at snapshot_at",
			"round_cohort":       "latest committed pool version per room/round; generated_at in [period_start,period_end)",
			"room_activation":    "both participants ready in one round, or trusted generated pool; zero-pool included",
			"both_users_voted":   "two distinct confirmed room members voted; separate from intent readiness",
			"completed_no_match": "no match, room exhausted in round 3, round 3 pool exhausted; round 1/2 exhaustion remains incomplete",
			"incomplete":         "all other nonmatched/nonclosed rooms, including waiting and expired; no automatic UX attribution",
			"outcome_precedence": "matched > closed > completed_no_match > incomplete; outcomes partition rooms",
			"recovered_match":    "match in round 2/3 with an earlier persisted exhausted round; not merely any later-round match",
			"invite_shared":      "creator share intention/attempt, never confirmed delivery; successful join is a server fact",
			"recipient_preview":  "noncreator server preview context, or preview linked to consumed invite before matching participant join; unverified previews excluded",
			"clicks":             "room match and same event, post-match, confirmed member at event time; tickets require server origin; not purchases",
			"test_data":          "all rooms included; demo or test/synthetic catalog sources flagged; no reliable room intent/test segment exists",
			"observation":        "current transaction snapshot, not historical replay; mutable state and 90-day telemetry retention limit historical reconstruction",
		},
		LegacyBaseline: recommendationLegacyBaseline{
			Cohort: "legacy micro-pilot as supplied in task; small N; not recomputed using v2",
			Ratios: []string{"9/19", "5/9", "5/19", "5/5", "2/5"},
			Source: "individual S4 brief; original primary data unavailable in this checkout",
		},
		UnverifiedClaims: []recommendationUnverifiedClaim{
			{Claim: "33.3%", Status: "excluded from confirmed results", Reason: "original formula and primary cohort unavailable"},
			{Claim: "36", Status: "excluded from confirmed results", Reason: "original unit, formula and primary cohort unavailable"},
			{Claim: "74.5%", Status: "excluded from confirmed results", Reason: "original formula and primary cohort unavailable"},
		},
	}
}

// Cohort and count semantics are unchanged from the original SQL functions.
// The database returns typed counts; JSON and N/D are assembled by Go.
const recommendationAnalyticsQuery = `WITH bounds AS (
 SELECT (((now() AT TIME ZONE 'UTC')::date - ($1::int - 1))::timestamp AT TIME ZONE 'UTC') AS period_start,
        (((now() AT TIME ZONE 'UTC')::date + 1)::timestamp AT TIME ZONE 'UTC') AS period_end
), cohort AS (
 SELECT * FROM analytics_recommendation_rooms WHERE created_at >= (SELECT period_start FROM bounds) AND created_at < (SELECT period_end FROM bounds)
), round_cohort AS (
 SELECT * FROM analytics_recommendation_rounds WHERE generated_at >= (SELECT period_start FROM bounds) AND generated_at < (SELECT period_end FROM bounds)
), room_counts AS (
 SELECT count(*) AS room_created,
        count(*) FILTER (WHERE second_user_joined_at IS NOT NULL) AS second_user_joined,
        count(*) FILTER (WHERE both_intents_ready_at IS NOT NULL) AS both_intents_ready,
        count(*) FILTER (WHERE both_distinct_users_voted_at IS NOT NULL) AS both_distinct_users_voted,
        count(*) FILTER (WHERE room_matched_at IS NOT NULL) AS room_matched,
        count(*) FILTER (WHERE outcome = 'completed_no_match') AS completed_no_match,
        count(*) FILTER (WHERE outcome = 'incomplete') AS incomplete,
        count(*) FILTER (WHERE outcome = 'incomplete' AND expires_at <= now()) AS expired_incomplete,
        count(*) FILTER (WHERE outcome = 'closed') AS closed,
        count(*) FILTER (WHERE matched_round = 1) AS first_round_match,
        count(*) FILTER (WHERE recovered_match AND matched_round = 2) AS recovered_round_2,
        count(*) FILTER (WHERE recovered_match AND matched_round = 3) AS recovered_round_3,
        count(*) FILTER (WHERE invite_share_attempted) AS invite_share_attempt_rooms,
        count(*) FILTER (WHERE recipient_previewed) AS recipient_preview_rooms,
        count(*) FILTER (WHERE invite_share_attempted AND recipient_previewed) AS shared_recipient_preview_rooms,
        count(*) FILTER (WHERE match_event_open_clicked) AS match_event_open_click_rooms,
        coalesce(sum(match_event_open_clicks),0)::bigint AS match_event_open_clicks,
        count(*) FILTER (WHERE ticket_clicked) AS ticket_click_rooms,
        coalesce(sum(ticket_clicks),0)::bigint AS ticket_clicks,
        count(*) FILTER (WHERE contains_test_events) AS rooms_with_test_events
 FROM cohort
), round_counts AS (
 SELECT count(*) AS generated_rounds,
        count(*) FILTER (WHERE zero_pool) AS zero_pool_rounds,
        count(*) FILTER (WHERE NOT zero_pool) AS nonempty_rounds_generated,
        count(*) FILTER (WHERE nonempty_exhausted) AS nonempty_rounds_exhausted
 FROM round_cohort
)
SELECT now(), b.period_start, b.period_end, r.*, p.*
FROM room_counts r CROSS JOIN round_counts p CROSS JOIN bounds b`

func analyticsBuildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	revision, modified := "unknown", false
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			revision = setting.Value
		}
		if setting.Key == "vcs.modified" {
			modified = setting.Value == "true"
		}
	}
	if modified {
		revision += "+dirty"
	}
	return revision
}

const dashboardQuery = `
WITH bounds AS (
 SELECT (((now() AT TIME ZONE 'UTC')::date - ($1::int - 1))::timestamp AT TIME ZONE 'UTC') AS period_start,
        (((now() AT TIME ZONE 'UTC')::date + 1)::timestamp AT TIME ZONE 'UTC') AS period_end
), cohort AS (
 SELECT f.*
 FROM analytics_room_funnel_metrics f CROSS JOIN bounds b
 WHERE f.created_at >= b.period_start AND f.created_at < b.period_end
), cohort_vote_facts AS (
 SELECT c.room_id, min(v.created_at) AS first_vote_at,
        count(v.created_at) FILTER (WHERE c.matched_at IS NOT NULL AND v.created_at <= c.matched_at)::bigint AS votes_to_match
 FROM cohort c LEFT JOIN room_votes v ON v.room_id = c.room_id
 GROUP BY c.room_id
), room_daily AS (
 SELECT (created_at AT TIME ZONE 'UTC')::date AS metric_date,
        count(*)::bigint rooms_created,
        count(*) FILTER (WHERE invite_confirmed)::bigint rooms_invite_shared,
        count(*) FILTER (WHERE invite_opened)::bigint rooms_invite_opened,
        count(*) FILTER (WHERE invite_confirmed AND joined)::bigint rooms_joined,
        count(*) FILTER (WHERE invite_confirmed AND joined AND activated)::bigint activated_rooms,
        count(*) FILTER (WHERE invite_confirmed AND joined AND activated AND matched)::bigint rooms_matched,
        count(*) FILTER (WHERE invite_confirmed AND joined AND activated AND matched AND ticket_clicked)::bigint rooms_ticket_clicked,
        count(*) FILTER (WHERE invite_confirmed AND joined AND activated AND matched AND match_event_opened)::bigint rooms_match_opened,
        count(*) FILTER (WHERE match_shown)::bigint rooms_match_shown,
        count(*) FILTER (WHERE additional_members = 0)::bigint rooms_without_second_participant,
        extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY cvf.first_vote_at - cohort.created_at)
          FILTER (WHERE cvf.first_vote_at IS NOT NULL))::double precision median_time_to_first_vote_seconds,
        extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY matched_at - activated_at)
          FILTER (WHERE matched_at IS NOT NULL AND activated_at IS NOT NULL))::double precision median_time_to_match_seconds,
        percentile_cont(0.5) WITHIN GROUP (ORDER BY cvf.votes_to_match)
          FILTER (WHERE matched_at IS NOT NULL)::double precision median_votes_to_match
 FROM cohort JOIN cohort_vote_facts cvf USING (room_id)
 GROUP BY (cohort.created_at AT TIME ZONE 'UTC')::date
), daily AS (
 SELECT d.metric_date AS date, d.active_users,
        coalesce(r.rooms_created, 0) AS rooms_created,
        coalesce(r.rooms_invite_shared, 0) AS rooms_invite_shared,
        coalesce(r.rooms_invite_opened, 0) AS rooms_invite_opened,
        coalesce(r.rooms_joined, 0) AS rooms_joined,
        coalesce(r.activated_rooms, 0) AS activated_rooms,
        coalesce(r.rooms_matched, 0) AS rooms_matched,
        coalesce(r.rooms_ticket_clicked, 0) AS rooms_ticket_clicked,
        coalesce(r.rooms_ticket_clicked, 0) AS rooms_ticket_transitions,
        coalesce(r.rooms_match_opened, 0) AS rooms_match_opened,
        d.room_creation_rate,
        d.active_users AS room_creation_rate_denominator,
        r.rooms_created AS invite_share_rate_denominator,
        r.rooms_invite_shared AS invite_open_rate_denominator,
        r.rooms_invite_shared AS invite_join_conversion_denominator,
        r.rooms_created AS room_activation_rate_denominator,
        r.activated_rooms AS match_rate_denominator,
        r.rooms_matched AS ticket_transition_rate_denominator,
        r.rooms_created AS rooms_without_second_participant_rate_denominator,
        r.rooms_invite_shared::numeric / nullif(r.rooms_created,0) AS invite_share_rate,
        r.rooms_invite_opened::numeric / nullif(r.rooms_invite_shared,0) AS invite_open_rate,
        r.rooms_joined::numeric / nullif(r.rooms_invite_shared,0) AS invite_join_conversion,
        r.activated_rooms::numeric / nullif(r.rooms_created,0) AS room_activation_rate,
        r.rooms_matched::numeric / nullif(r.activated_rooms,0) AS match_rate,
        r.rooms_ticket_clicked::numeric / nullif(r.rooms_matched,0) AS match_ticket_ctr,
        r.rooms_without_second_participant,
        r.rooms_without_second_participant::numeric / nullif(r.rooms_created,0) AS rooms_without_second_participant_rate,
        r.median_time_to_first_vote_seconds,
        r.median_time_to_match_seconds,
        extract(epoch FROM d.median_time_to_join)::double precision AS median_time_to_join_seconds,
        extract(epoch FROM d.p75_time_to_join)::double precision AS p75_time_to_join_seconds,
        extract(epoch FROM d.p90_time_to_join)::double precision AS p90_time_to_join_seconds,
        r.median_votes_to_match AS median_swipes_to_match,
        r.median_votes_to_match, d.second_room_rate_7d, d.second_room_rate_30d,
        d.recommendation_top10_like_rate, coalesce(r.rooms_match_shown, 0) AS rooms_match_shown
 FROM analytics_daily_product_metrics d
 LEFT JOIN room_daily r ON r.metric_date = d.metric_date
 CROSS JOIN bounds b
 WHERE d.metric_date >= (b.period_start AT TIME ZONE 'UTC')::date
   AND d.metric_date < (b.period_end AT TIME ZONE 'UTC')::date
 ORDER BY d.metric_date
), room_period AS (
 SELECT extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY cvf.first_vote_at - cohort.created_at)
          FILTER (WHERE cvf.first_vote_at IS NOT NULL))::double precision median_time_to_first_vote_seconds,
        extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY matched_at - activated_at)
          FILTER (WHERE matched_at IS NOT NULL AND activated_at IS NOT NULL))::double precision median_time_to_match_seconds,
        percentile_cont(0.5) WITHIN GROUP (ORDER BY cvf.votes_to_match)
          FILTER (WHERE matched_at IS NOT NULL)::double precision median_votes_to_match,
        extract(epoch FROM percentile_cont(0.5) WITHIN GROUP (ORDER BY first_joined_at - created_at)
          FILTER (WHERE first_joined_at IS NOT NULL))::double precision median_time_to_join_seconds
 FROM cohort JOIN cohort_vote_facts cvf USING (room_id)
), second_room_period AS (
 SELECT count(*) FILTER (WHERE cohort.created_at <= now() - interval '7 days' AND has_second_7d)::bigint AS second_room_7d_numerator,
        count(*) FILTER (WHERE cohort.created_at <= now() - interval '7 days')::bigint AS second_room_7d_denominator,
        count(*) FILTER (WHERE cohort.created_at <= now() - interval '7 days' AND has_second_7d)::numeric /
          nullif(count(*) FILTER (WHERE cohort.created_at <= now() - interval '7 days'), 0) second_room_rate_7d,
        count(*) FILTER (WHERE cohort.created_at <= now() - interval '30 days' AND has_second_30d)::bigint AS second_room_30d_numerator,
        count(*) FILTER (WHERE cohort.created_at <= now() - interval '30 days')::bigint AS second_room_30d_denominator,
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
   FROM cohort first_room
   WHERE NOT EXISTS (SELECT 1 FROM analytics_room_metrics earlier
                     WHERE earlier.creator_user_id = first_room.creator_user_id
                       AND (earlier.created_at < first_room.created_at
                            OR (earlier.created_at = first_room.created_at AND earlier.room_id < first_room.room_id)))
 ) cohort
), daily_summary AS (
 SELECT (SELECT count(DISTINCT user_id) FROM analytics_events
          WHERE occurred_at >= (SELECT period_start FROM bounds)
            AND occurred_at < (SELECT period_end FROM bounds)) active_users,
        coalesce(sum(rooms_created),0)::bigint rooms_created,
        coalesce(sum(rooms_invite_shared),0)::bigint rooms_invite_shared,
        coalesce(sum(rooms_invite_opened),0)::bigint rooms_invite_opened,
        coalesce(sum(rooms_joined),0)::bigint rooms_joined,
        coalesce(sum(activated_rooms),0)::bigint activated_rooms,
        coalesce(sum(rooms_matched),0)::bigint rooms_matched,
        coalesce(sum(rooms_ticket_clicked),0)::bigint rooms_ticket_clicked,
        coalesce(sum(rooms_match_opened),0)::bigint rooms_match_opened,
        coalesce(sum(rooms_without_second_participant),0)::bigint rooms_without_second_participant,
        coalesce((SELECT sum(ticket_clicks) FROM cohort),0)::bigint ticket_clicks,
        sum(rooms_invite_shared)::numeric / nullif(sum(rooms_created),0) invite_share_rate,
        (SELECT count(DISTINCT creator_user_id)::numeric / nullif((SELECT count(DISTINCT user_id) FROM analytics_events
          WHERE occurred_at >= (SELECT period_start FROM bounds) AND occurred_at < (SELECT period_end FROM bounds)),0)
           FROM cohort) room_creation_rate,
        sum(rooms_invite_opened)::numeric / nullif(sum(rooms_invite_shared),0) invite_open_rate,
        sum(rooms_joined)::numeric / nullif(sum(rooms_invite_shared),0) invite_join_conversion,
        sum(activated_rooms)::numeric / nullif(sum(rooms_created),0) room_activation_rate,
        sum(rooms_matched)::numeric / nullif(sum(activated_rooms),0) match_rate,
        sum(rooms_matched)::numeric / nullif(sum(rooms_created),0) created_match_conversion,
        sum(rooms_ticket_clicked)::numeric / nullif(sum(rooms_matched),0) match_ticket_ctr,
        sum(rooms_match_opened)::numeric / nullif(sum(rooms_matched),0) match_event_open_ctr,
        sum(rooms_created)::bigint invite_share_rate_denominator,
        sum(rooms_invite_shared)::bigint invite_open_rate_denominator,
        sum(rooms_invite_shared)::bigint invite_join_conversion_denominator,
        sum(rooms_created)::bigint room_activation_rate_denominator,
        sum(activated_rooms)::bigint match_rate_denominator,
        sum(rooms_created)::bigint created_match_conversion_denominator,
        sum(rooms_matched)::bigint match_ticket_ctr_denominator,
        sum(rooms_matched)::bigint match_event_open_ctr_denominator,
        sum(rooms_created)::bigint rooms_without_second_participant_rate_denominator,
        sum(rooms_matched)::bigint match_ticket_transition_rate_denominator,
        (SELECT count(DISTINCT user_id) FROM analytics_events
          WHERE occurred_at >= (SELECT period_start FROM bounds)
            AND occurred_at < (SELECT period_end FROM bounds))::bigint AS room_creation_rate_denominator,
        coalesce(sum(rooms_ticket_clicked),0)::bigint AS ticket_transitions,
        sum(rooms_ticket_clicked)::numeric / nullif(sum(rooms_matched),0) AS match_ticket_transition_rate,
        sum(rooms_without_second_participant)::numeric / nullif(sum(rooms_created),0) AS rooms_without_second_participant_rate,
        sum(rooms_created)::bigint rooms_created_denominator,
        sum(rooms_invite_shared)::bigint invite_open_denominator,
        sum(rooms_invite_shared)::bigint invite_join_denominator,
        sum(rooms_created)::bigint activation_denominator,
        sum(activated_rooms)::bigint match_denominator,
        sum(rooms_matched)::bigint ticket_transition_denominator,
        sum(rooms_matched)::bigint match_open_denominator,
        (SELECT count(*) FILTER (WHERE activated)::bigint FROM cohort) activated_rooms_denominator,
        (SELECT count(*) FILTER (WHERE state = 'exhausted' AND activated AND NOT matched)::bigint FROM cohort) no_match_numerator,
        (SELECT count(*) FILTER (WHERE activated)::bigint FROM cohort) no_match_denominator,
        (SELECT count(*) FILTER (WHERE state = 'exhausted' AND activated)::bigint FROM cohort) pool_exhausted_numerator,
        (SELECT count(*) FILTER (WHERE activated)::bigint FROM cohort) pool_exhausted_denominator,
        (SELECT count(*) FILTER (WHERE state = 'exhausted' AND activated AND NOT matched)::numeric /
                 nullif(count(*) FILTER (WHERE activated),0) FROM cohort) no_match_rate,
        (SELECT count(*) FILTER (WHERE state = 'exhausted' AND activated)::numeric /
                 nullif(count(*) FILTER (WHERE activated),0) FROM cohort) pool_exhausted_rate,
        (SELECT median_time_to_match_seconds FROM room_period) median_time_to_match_seconds,
        (SELECT median_time_to_first_vote_seconds FROM room_period) median_time_to_first_vote_seconds,
        (SELECT median_votes_to_match FROM room_period) median_swipes_to_match,
        (SELECT median_votes_to_match FROM room_period) median_votes_to_match,
        (SELECT median_time_to_join_seconds FROM room_period) median_time_to_join_seconds,
        (SELECT second_room_rate_7d FROM second_room_period) second_room_rate_7d,
        (SELECT second_room_rate_30d FROM second_room_period) second_room_rate_30d,
        (SELECT second_room_7d_numerator FROM second_room_period) second_room_7d_numerator,
        (SELECT second_room_7d_denominator FROM second_room_period) second_room_7d_denominator,
        (SELECT second_room_30d_numerator FROM second_room_period) second_room_30d_numerator,
        (SELECT second_room_30d_denominator FROM second_room_period) second_room_30d_denominator,
        (SELECT count(*)::bigint FROM cohort) rooms_without_second_participant_denominator
 FROM daily
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
 'metrics_version', 'legacy-room-funnel-v1',
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
