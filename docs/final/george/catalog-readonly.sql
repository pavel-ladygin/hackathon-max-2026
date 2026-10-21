-- Owner-operated only. Run against an explicitly approved snapshot/export DB.
-- Does not connect itself, import, migrate or mutate data. Never paste a DSN
-- or raw provider error_text into a report.
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '30s';
SET LOCAL lock_timeout = '3s';

-- Q1: raw catalog coverage. URL syntax is NOT a live reachability/allowlist check.
-- Restrict to Moscow; change the literal only for another approved city.
WITH scoped AS (
    SELECT e.*, v.city_id, v.address,
           v.latitude BETWEEN -90 AND 90 AND v.longitude BETWEEN -180 AND 180 AS coordinates_ok,
           EXISTS (SELECT 1 FROM event_images i WHERE i.event_id=e.id AND i.role='card') AS poster,
           EXISTS (SELECT 1 FROM event_categories c WHERE c.event_id=e.id AND c.category_slug IN
             ('concerts','standup','cinema','theatre','exhibitions','sports','food','parties','festivals','walks','other')) AS category
    FROM events e JOIN venues v ON v.id=e.venue_id
    WHERE v.city_id='a0f625ee-2154-5a45-8afe-37adf955ec24' AND NOT e.is_demo
)
SELECT current_timestamp AS measured_at, city_id, source,
       count(*) AS total,
       count(*) FILTER (WHERE starts_at>current_timestamp AND starts_at<current_timestamp+interval '90 days') AS future_90d,
       count(*) FILTER (WHERE starts_at>current_timestamp AND starts_at<current_timestamp+interval '90 days'
          AND status='published' AND provider_active AND ticket_available
          AND ticket_url ~ '^https://[^/@[:space:]]+(/|$)') AS future_published_active_ticket_syntax,
       count(*) FILTER (WHERE poster) AS poster_n, count(*) AS poster_d,
       count(*) FILTER (WHERE coordinates_ok) AS coordinates_n, count(*) AS coordinates_d,
       count(*) FILTER (WHERE category) AS category_n, count(*) AS category_d,
       count(*) FILTER (WHERE price_from_minor IS NOT NULL) AS known_price_n, count(*) AS known_price_d,
       count(*) FILTER (WHERE ends_at IS NULL OR ends_at>starts_at) AS valid_time_n, count(*) AS valid_time_d,
       count(*) FILTER (WHERE length(trim(address))>0) AS address_n, count(*) AS address_d
FROM scoped GROUP BY city_id,source ORDER BY city_id,source;

-- Q2: state-level latest sync. "Partial" is inferred, not a schema state.
-- No raw error payloads, tokens, source endpoint URLs or user records.
SELECT city_id, provider, state, errors>0 AS has_record_errors,
       max(started_at) AS last_attempt_at, max(completed_at) AS last_completed_at,
       count(*) AS runs, sum(errors) AS record_errors
FROM provider_sync_runs
WHERE city_id='a0f625ee-2154-5a45-8afe-37adf955ec24'
GROUP BY city_id,provider,state,errors>0
ORDER BY city_id,provider,state,has_record_errors;

-- Q3: natural-key duplicates (normally prevented by the UNIQUE constraint).
SELECT e.source, count(*) AS duplicate_keys
FROM (
    SELECT e.source,e.external_id
    FROM events e JOIN venues v ON v.id=e.venue_id
    WHERE v.city_id='a0f625ee-2154-5a45-8afe-37adf955ec24'
    GROUP BY e.source,e.external_id HAVING count(*)>1
) e GROUP BY e.source;

-- Q4: non-secret city identity preflight before deployment of migration 000031.
SELECT id,name,timezone,center_lat,center_lng FROM cities
WHERE id='3d3c83de-691b-510e-85e5-79685c4012ee'
   OR lower(translate(trim(name), 'КАЗНЬ', 'казнь')) IN ('казань','kazan','kazan''','qazan');
ROLLBACK;
