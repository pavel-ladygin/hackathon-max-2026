package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/timepad"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

const idlePollInterval = time.Minute

func parseExternalID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid Timepad event ID %q", value)
	}
	return id, nil
}

func runContinuous(ctx context.Context, db *store.Pool, client *timepad.Client, limit int) error {
	pacer := requestPacer{interval: 1100 * time.Millisecond}
	for {
		candidates, err := loadDueCandidates(ctx, db, limit)
		if err != nil {
			fmt.Printf("continuous status=db_error error=%q retry_in=%s\n", err, idlePollInterval)
			if err := waitContext(ctx, idlePollInterval); err != nil {
				return err
			}
			continue
		}
		if len(candidates) == 0 {
			if err := waitContext(ctx, idlePollInterval); err != nil {
				return err
			}
			continue
		}
		fmt.Printf("continuous batch=%d\n", len(candidates))
		cache := make(posterCache)
		for _, item := range candidates {
			if err := processCandidate(ctx, db, client, cache, &pacer, item); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				fmt.Printf("event=%s status=processing_error error=%q\n", item.id, err)
			}
		}
	}
}

func loadDueCandidates(ctx context.Context, db *store.Pool, limit int) ([]candidate, error) {
	rows, err := db.Query(ctx, `
		SELECT e.id, e.external_id, e.ticket_url
		FROM events e
		LEFT JOIN timepad_poster_recovery r ON r.event_id = e.id
		WHERE e.source = 'timepad' AND e.status = 'published'
		  AND e.starts_at > now() AND e.is_demo = false AND e.ticket_url IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM event_images i WHERE i.event_id = e.id)
		  AND (r.event_id IS NULL OR (r.next_attempt_at <= now() AND r.outcome NOT IN ('inserted','already_filled')))
		ORDER BY COALESCE(r.next_attempt_at, now()), e.starts_at, e.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]candidate, 0, limit)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.externalID, &item.url); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func processCandidate(ctx context.Context, db *store.Pool, client *timepad.Client, cache posterCache, pacer *requestPacer, item candidate) error {
	parsedID, err := parseExternalID(item.externalID)
	if err != nil {
		return recordAttempt(ctx, db, item.id, "error", err.Error(), retryDelay)
	}
	if err := pacer.wait(ctx); err != nil {
		return err
	}
	canonicalID, err := client.ResolveCanonicalEventID(ctx, parsedID, item.url)
	if err != nil {
		return recordAttempt(ctx, db, item.id, "error", err.Error(), retryDelay)
	}
	if canonicalID == 0 {
		fmt.Printf("event=%s external_id=%s status=no_redirect\n", item.id, item.externalID)
		return recordAttempt(ctx, db, item.id, "no_redirect", "", 24*time.Hour)
	}
	images, err := cache.get(ctx, canonicalID, func(ctx context.Context, id int64) ([]providers.NormalizedImage, error) {
		if err := pacer.wait(ctx); err != nil {
			return nil, err
		}
		return client.FetchEventPoster(ctx, id)
	})
	if err != nil {
		return recordAttempt(ctx, db, item.id, "error", err.Error(), retryDelay)
	}
	if len(images) == 0 {
		fmt.Printf("event=%s external_id=%s canonical_id=%d status=no_poster\n", item.id, item.externalID, canonicalID)
		return recordAttempt(ctx, db, item.id, "no_poster", "", 7*24*time.Hour)
	}
	inserted, err := insertPoster(ctx, db, item.id, images[0].URL)
	if err != nil {
		return recordAttempt(ctx, db, item.id, "error", err.Error(), retryDelay)
	}
	if inserted {
		fmt.Printf("event=%s external_id=%s canonical_id=%d status=inserted image_url=%s\n", item.id, item.externalID, canonicalID, images[0].URL)
		return recordAttempt(ctx, db, item.id, "inserted", "", 0)
	}
	fmt.Printf("event=%s status=already_filled\n", item.id)
	return recordAttempt(ctx, db, item.id, "already_filled", "", 0)
}

func insertPoster(ctx context.Context, db *store.Pool, eventID uuid.UUID, imageURL string) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	// Hash collisions only serialize unrelated events; they cannot compromise correctness.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, eventID.String()); err != nil {
		return false, err
	}
	// The provider importer locks the event row during its upsert. Locking it
	// here ensures the existence check also sees any concurrent image update.
	var lockedID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM events WHERE id = $1 AND source = 'timepad' FOR UPDATE`, eventID).Scan(&lockedID); err != nil {
		return false, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM event_images WHERE event_id = $1)`, eventID).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, tx.Commit(ctx)
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO event_images (id, event_id, url, role, position)
		SELECT $1, e.id, $2, 'card', 0 FROM events e
		WHERE e.id = $3 AND e.source = 'timepad'
		  AND NOT EXISTS (SELECT 1 FROM event_images i WHERE i.event_id = e.id)`, uuid.New(), imageURL, eventID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return result.RowsAffected() > 0, nil
}

func recordAttempt(ctx context.Context, db *store.Pool, eventID uuid.UUID, outcome, errorText string, delay time.Duration) error {
	if len(errorText) > 2000 {
		errorText = errorText[:2000]
	}
	_, err := db.Exec(ctx, `
		INSERT INTO timepad_poster_recovery (event_id, attempts, next_attempt_at, outcome, last_error, updated_at)
		VALUES ($1, 1, now() + ($4::bigint * interval '1 microsecond'), $2, NULLIF($3, ''), now())
		ON CONFLICT (event_id) DO UPDATE SET
			attempts = timepad_poster_recovery.attempts + 1,
			next_attempt_at = now() + ($4::bigint * interval '1 microsecond'),
			outcome = EXCLUDED.outcome, last_error = EXCLUDED.last_error, updated_at = now()`, eventID, outcome, errorText, delay.Microseconds())
	return err
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
