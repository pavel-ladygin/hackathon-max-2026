package main

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/timepad"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

const (
	idlePollInterval       = time.Minute
	continuousWorkers      = 8
	continuousAPIPace      = 3 * time.Second
	timepadSyncPoll        = 5 * time.Second
	timepadSyncFreshWindow = 30 * time.Minute
	timepadSyncQuietWindow = time.Minute
)

func parseExternalID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid Timepad event ID %q", value)
	}
	return id, nil
}

func runContinuous(ctx context.Context, db *store.Pool, client *timepad.Client, limit int) error {
	pacer := &serializedPacer{interval: continuousAPIPace}
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
		fmt.Printf("continuous batch=%d workers=%d\n", len(candidates), continuousWorkers)
		cache := newContinuousPosterCache()
		if err := runCandidateBatch(ctx, candidates, continuousWorkers, func(ctx context.Context, item candidate) {
			if err := processContinuousCandidate(ctx, db, client, cache, pacer, item); err != nil && ctx.Err() == nil {
				fmt.Printf("event=%s status=processing_error error=%q\n", item.id, err)
			}
		}); err != nil {
			return err
		}
		// Do not load the next batch until all writes and recovery outcomes from
		// this batch have finished.
	}
}

func waitForTimepadSync(ctx context.Context, db *store.Pool) error {
	for {
		var syncing bool
		err := db.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM provider_sync_runs
				WHERE provider = 'timepad'
				  AND ((state = 'running' AND started_at > now() - ($1::bigint * interval '1 second'))
				    OR (completed_at IS NOT NULL AND completed_at > now() - ($2::bigint * interval '1 second')))
			)`, int64(timepadSyncFreshWindow/time.Second), int64(timepadSyncQuietWindow/time.Second)).Scan(&syncing)
		if err != nil {
			return fmt.Errorf("check Timepad sync status: %w", err)
		}
		if !syncing {
			return nil
		}
		if err := waitContext(ctx, timepadSyncPoll); err != nil {
			return err
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

func runCandidateBatch(ctx context.Context, candidates []candidate, limit int, process func(context.Context, candidate)) error {
	if limit < 1 {
		limit = 1
	}
	if limit > len(candidates) {
		limit = len(candidates)
	}
	if limit == 0 {
		return ctx.Err()
	}
	jobs := make(chan candidate)
	var workers sync.WaitGroup
	for range limit {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					continue
				}
				process(ctx, item)
			}
		}()
	}

sendLoop:
	for _, item := range candidates {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- item:
		}
	}
	close(jobs)
	workers.Wait()
	return ctx.Err()
}

func processContinuousCandidate(ctx context.Context, db *store.Pool, client *timepad.Client, cache *continuousPosterCache, pacer *serializedPacer, item candidate) error {
	parsedID, err := parseExternalID(item.externalID)
	if err != nil {
		return recordAttempt(ctx, db, item.id, "error", err.Error(), retryDelay)
	}
	canonicalID, err := client.ResolveCanonicalEventID(ctx, parsedID, item.url)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return recordAttempt(ctx, db, item.id, "error", err.Error(), retryDelay)
	}
	if canonicalID == 0 {
		fmt.Printf("event=%s external_id=%s status=no_redirect\n", item.id, item.externalID)
		return recordAttempt(ctx, db, item.id, "no_redirect", "", 24*time.Hour)
	}
	images, err := cache.get(ctx, canonicalID, func(ctx context.Context, id int64) ([]providers.NormalizedImage, error) {
		var fetched []providers.NormalizedImage
		err := pacer.request(ctx, func(ctx context.Context) error {
			return waitForTimepadSync(ctx, db)
		}, func(ctx context.Context) error {
			var fetchErr error
			fetched, fetchErr = client.FetchEventPoster(ctx, id)
			return fetchErr
		})
		if err != nil {
			return nil, err
		}
		return fetched, nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return recordAttempt(ctx, db, item.id, "error", err.Error(), retryDelay)
	}
	if len(images) == 0 {
		fmt.Printf("event=%s external_id=%s canonical_id=%d status=no_poster\n", item.id, item.externalID, canonicalID)
		return recordAttempt(ctx, db, item.id, "no_poster", "", 7*24*time.Hour)
	}
	inserted, err := insertPoster(ctx, db, item.id, images[0].URL)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return recordAttempt(ctx, db, item.id, "error", err.Error(), retryDelay)
	}
	if inserted {
		fmt.Printf("event=%s external_id=%s canonical_id=%d status=inserted image_url=%s\n", item.id, item.externalID, canonicalID, images[0].URL)
		return recordAttempt(ctx, db, item.id, "inserted", "", 0)
	}
	fmt.Printf("event=%s status=already_filled\n", item.id)
	return recordAttempt(ctx, db, item.id, "already_filled", "", 0)
}

type serializedPacer struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func (p *serializedPacer) request(ctx context.Context, before func(context.Context) error, request func(context.Context) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.next.IsZero() {
		if err := waitContext(ctx, time.Until(p.next)); err != nil {
			return err
		}
	}
	if before != nil {
		if err := before(ctx); err != nil {
			return err
		}
	}
	p.next = time.Now().Add(p.interval)
	return request(ctx)
}

type posterFetch struct {
	done   chan struct{}
	images []providers.NormalizedImage
	err    error
}

type continuousPosterCache struct {
	mu      sync.Mutex
	entries map[int64]*posterFetch
}

func newContinuousPosterCache() *continuousPosterCache {
	return &continuousPosterCache{entries: make(map[int64]*posterFetch)}
}

func (cache *continuousPosterCache) get(ctx context.Context, canonicalID int64, fetch func(context.Context, int64) ([]providers.NormalizedImage, error)) ([]providers.NormalizedImage, error) {
	cache.mu.Lock()
	if entry, ok := cache.entries[canonicalID]; ok {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-entry.done:
			return entry.images, entry.err
		}
	}
	entry := &posterFetch{done: make(chan struct{})}
	cache.entries[canonicalID] = entry
	cache.mu.Unlock()

	entry.images, entry.err = fetch(ctx, canonicalID)
	cache.mu.Lock()
	if entry.err != nil {
		delete(cache.entries, canonicalID)
	}
	close(entry.done)
	cache.mu.Unlock()
	return entry.images, entry.err
}

func insertPoster(ctx context.Context, db *store.Pool, eventID uuid.UUID, imageURL string) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, eventID.String()); err != nil {
		return false, err
	}
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
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
