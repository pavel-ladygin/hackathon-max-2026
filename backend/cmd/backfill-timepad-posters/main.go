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
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	token := strings.TrimSpace(os.Getenv("TIMEPAD_TOKEN"))
	if *continuous && token == "" {
		fmt.Println("continuous status=disabled reason=TIMEPAD_TOKEN_not_configured")
		return nil
	}
	if databaseURL == "" || token == "" {
		return errors.New("DATABASE_URL and TIMEPAD_TOKEN are required")
	}
	baseURL := strings.TrimSpace(os.Getenv("TIMEPAD_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.timepad.ru/v1"
	}
	client, err := timepad.NewClient(timepad.Options{BaseURL: baseURL, Token: token, Timeout: 10 * time.Second, PageSize: 100})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		return fmt.Errorf("check migrations: %w", err)
	}
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
	pacer := requestPacer{interval: 1100 * time.Millisecond}
	for _, item := range candidates {
		externalID, err := strconv.ParseInt(item.externalID, 10, 64)
		if err != nil || externalID <= 0 {
			failed++
			fmt.Printf("event=%s external_id=%s status=invalid_id\n", item.id, item.externalID)
			continue
		}
		if err := pacer.wait(ctx); err != nil {
			return err
		}
		canonicalID, err := client.ResolveCanonicalEventID(ctx, externalID, item.url)
		if err != nil {
			failed++
			fmt.Printf("event=%s external_id=%s status=error error=%q\n", item.id, item.externalID, err)
			continue
		}
		if canonicalID == 0 {
			fmt.Printf("event=%s external_id=%s status=no_redirect\n", item.id, item.externalID)
			continue
		}
		images, err := cache.get(ctx, canonicalID, func(ctx context.Context, id int64) ([]providers.NormalizedImage, error) {
			if err := pacer.wait(ctx); err != nil {
				return nil, err
			}
			return client.FetchEventPoster(ctx, id)
		})
		if err != nil {
			failed++
			fmt.Printf("event=%s external_id=%s canonical_id=%d status=error error=%q\n", item.id, item.externalID, canonicalID, err)
			continue
		}
		if len(images) == 0 {
			fmt.Printf("event=%s external_id=%s canonical_id=%d status=no_poster\n", item.id, item.externalID, canonicalID)
			continue
		}
		found++
		if !*apply {
			fmt.Printf("event=%s external_id=%s canonical_id=%d status=found image_url=%s\n", item.id, item.externalID, canonicalID, images[0].URL)
			continue
		}
		inserted, err := insertPoster(ctx, db, item.id, images[0].URL)
		if err != nil {
			failed++
			fmt.Printf("event=%s external_id=%s status=write_error error=%q\n", item.id, item.externalID, err)
			continue
		}
		if inserted {
			written++
			fmt.Printf("event=%s external_id=%s canonical_id=%d status=inserted image_url=%s\n", item.id, item.externalID, canonicalID, images[0].URL)
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
