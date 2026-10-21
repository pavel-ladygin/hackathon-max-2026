// Command backfill-timepad-posters finds missing Timepad occurrence posters.
// It reports candidates by default and writes only when --apply is specified.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/timepad"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

const maxBatch = 50
const retryDelay = time.Hour

type candidate struct {
	id         uuid.UUID
	externalID string
	url        string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("backfill-timepad-posters", flag.ContinueOnError)
	limit := flags.Int("limit", 20, "maximum number of missing-image events to inspect (1-50)")
	apply := flags.Bool("apply", false, "insert recovered posters; default is read-only")
	externalIDs := flags.String("external-ids", "", "comma-separated Timepad event IDs for a repeatable pilot")
	continuous := flags.Bool("continuous", false, "continuously recover due candidates; requires --apply")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateMode(*limit, *externalIDs, *apply, *continuous, flags.NArg()); err != nil {
		return err
	}
	selected := []string{}
	if strings.TrimSpace(*externalIDs) != "" {
		for _, value := range strings.Split(*externalIDs, ",") {
			id := strings.TrimSpace(value)
			parsed, err := strconv.ParseInt(id, 10, 64)
			if err != nil || parsed <= 0 {
				return fmt.Errorf("invalid Timepad event ID %q", id)
			}
			selected = append(selected, id)
		}
		if len(selected) > maxBatch || len(selected) > *limit {
			return errors.New("external ID count must not exceed --limit or 50")
		}
	}
	enabled, err := config.ProviderEnabled("EVENT_SYNC_TIMEPAD_ENABLED")
	if err != nil {
		return err
	}
	if !enabled {
		fmt.Println("provider=timepad status=disabled")
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, cfg.EventSyncMaxRunDuration)
	defer cancel()
	client, err := timepad.NewClient(timepad.Options{
		BaseURL: cfg.TimepadBaseURL, Token: cfg.TimepadToken, Timeout: cfg.TimepadTimeout, PageSize: cfg.TimepadPageSize,
		MaxRequestsPerMinute: cfg.TimepadMaxRequestsPerMinute,
		MaxPagesPerRun:       cfg.TimepadMaxPagesPerRun, MaxRunDuration: cfg.EventSyncMaxRunDuration, UpsertOnly: cfg.EventSyncUpsertOnly,
	})
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		return fmt.Errorf("check migrations: %w", err)
	}
	lock, acquired, err := providers.TryAcquireProviderSyncLock(ctx, db, "timepad")
	if err != nil {
		return errors.New("acquire timepad sync lock failed")
	}
	if !acquired {
		fmt.Println("provider_sync_skipped_already_running provider=timepad")
		return nil
	}
	defer func() { _ = lock.Release() }()
	if *continuous {
		return runContinuous(ctx, db, client, *limit)
	}
	rows, err := db.Query(ctx, `
		SELECT e.id, e.external_id, e.ticket_url
		FROM events e
		WHERE e.source = 'timepad' AND e.status = 'published'
		  AND e.starts_at > now() AND e.is_demo = false
		  AND e.ticket_url IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM event_images i WHERE i.event_id = e.id)
		  AND (cardinality($2::text[]) = 0 OR e.external_id = ANY($2::text[]))
		ORDER BY e.starts_at, e.id
		LIMIT $1`, *limit, selected)
	if err != nil {
		return fmt.Errorf("list missing posters: %w", err)
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.externalID, &item.url); err != nil {
			rows.Close()
			return fmt.Errorf("scan candidate: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read candidates: %w", err)
	}
	rows.Close()

	found, written, failed := 0, 0, 0
	cache := make(posterCache)
	for _, item := range candidates {
		externalID, err := strconv.ParseInt(item.externalID, 10, 64)
		if err != nil || externalID <= 0 {
			failed++
			fmt.Printf("event=%s external_id=%s status=invalid_id\n", item.id, item.externalID)
			continue
		}
		canonicalID, err := client.ResolveCanonicalEventID(ctx, externalID, item.url)
		if err != nil {
			failed++
			fmt.Printf("event=%s external_id=%s status=error error_code=%s\n", item.id, item.externalID, providers.SyncErrorCode(err))
			if providers.IsBlockingError(err) {
				return providers.SafeError(providers.SyncErrorCode(err))
			}
			continue
		}
		if canonicalID == 0 {
			fmt.Printf("event=%s external_id=%s status=no_redirect\n", item.id, item.externalID)
			continue
		}
		images, err := cache.get(ctx, canonicalID, func(ctx context.Context, id int64) ([]providers.NormalizedImage, error) {
			return client.FetchEventPoster(ctx, id)
		})
		if err != nil {
			failed++
			fmt.Printf("event=%s external_id=%s canonical_id=%d status=error error_code=%s\n", item.id, item.externalID, canonicalID, providers.SyncErrorCode(err))
			if providers.IsBlockingError(err) {
				return providers.SafeError(providers.SyncErrorCode(err))
			}
			continue
		}
		if len(images) == 0 {
			fmt.Printf("event=%s external_id=%s canonical_id=%d status=no_poster\n", item.id, item.externalID, canonicalID)
			continue
		}
		found++
		if !*apply {
			fmt.Printf("event=%s external_id=%s canonical_id=%d status=found\n", item.id, item.externalID, canonicalID)
			continue
		}
		inserted, err := insertPoster(ctx, db, item.id, images[0].URL)
		if err != nil {
			failed++
			fmt.Printf("event=%s external_id=%s status=write_error error_code=provider_persistence_error\n", item.id, item.externalID)
			continue
		}
		if inserted {
			written++
			fmt.Printf("event=%s external_id=%s canonical_id=%d status=inserted\n", item.id, item.externalID, canonicalID)
		} else {
			fmt.Printf("event=%s external_id=%s status=already_filled\n", item.id, item.externalID)
		}
	}
	fmt.Printf("inspected=%d found=%d inserted=%d errors=%d apply=%t\n", len(candidates), found, written, failed, *apply)
	if failed > 0 {
		return fmt.Errorf("%d candidates could not be checked or written", failed)
	}
	return nil
}

func validateMode(limit int, externalIDs string, apply, continuous bool, positionalArgs int) error {
	if positionalArgs != 0 || limit < 1 || limit > maxBatch {
		return errors.New("usage: backfill-timepad-posters [--limit 1-50] [--external-ids id,id] [--apply] [--continuous]")
	}
	if continuous && (!apply || strings.TrimSpace(externalIDs) != "") {
		return errors.New("--continuous requires --apply and cannot be combined with --external-ids")
	}
	return nil
}

// requestPacer spaces every outbound Timepad request, including redirects and
// parent API lookups, rather than merely spacing events in the batch.
type requestPacer struct {
	interval time.Duration
	next     time.Time
}

func (p *requestPacer) wait(ctx context.Context) error {
	if !p.next.IsZero() {
		delay := time.Until(p.next)
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	p.next = time.Now().Add(p.interval)
	return nil
}
