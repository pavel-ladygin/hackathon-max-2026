package timepad

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type EventStore = providers.EventStore
type ImportStats = providers.ImportStats

type normalizationEmptyError struct{}

func (normalizationEmptyError) Error() string {
	return "timepad normalization produced no upsertable events"
}
func (normalizationEmptyError) SyncErrorCode() string { return "timepad_normalization_empty" }

// Import fetches every Timepad page in the configured horizon and persists
// valid normalized events through the shared provider ingestion path.
func (c *Client) Import(ctx context.Context, cityID uuid.UUID, store providers.SyncStore, reportError func(error)) (stats ImportStats, importErr error) {
	return c.ImportPages(ctx, cityID, store, reportError, 0)
}

// ImportPages imports at most maxPages pages. A positive limit creates an
// upsert-only audit run, so a diagnostic/manual partial import never
// reconciles (deactivates) events missing from the truncated result set.
func (c *Client) ImportPages(ctx context.Context, cityID uuid.UUID, store providers.SyncStore, reportError func(error), maxPages int) (stats ImportStats, importErr error) {
	if c == nil {
		return ImportStats{}, errors.New("timepad client is required")
	}
	if maxPages < 0 {
		return ImportStats{}, errors.New("timepad max pages must not be negative")
	}
	if store == nil {
		return ImportStats{}, errors.New("provider event store is required")
	}
	posterStore := &importPosterStore{
		client: c, store: store, reportError: reportError,
		postersByCanonicalID: make(map[int64][]providers.NormalizedImage),
		resolveCanonicalID:   c.ResolveCanonicalEventID,
		fetchEventPoster:     c.FetchEventPoster,
	}
	ingestion, err := providers.NewIngestion(cityID, posterStore, reportError)
	if err != nil {
		return ImportStats{}, err
	}
	startsAtMin := c.now().UTC()
	startsAtMax := startsAtMin.Add(importHorizon)
	runID, err := store.BeginSyncRun(ctx, providers.SyncRunStart{
		Provider: timepadSource, CityID: cityID, WindowStart: startsAtMin, WindowEnd: startsAtMax, UpsertOnly: maxPages > 0,
	})
	if err != nil {
		return ImportStats{}, fmt.Errorf("begin timepad sync run: %w", err)
	}
	defer func() {
		stats.SyncRunID = runID
		reconciled, finishErr := providers.FinalizeSyncRun(ctx, store, runID, stats, importErr)
		stats.Reconciled = reconciled
		if finishErr != nil {
			finishErr = fmt.Errorf("finish timepad sync run: %w", finishErr)
			if importErr != nil {
				importErr = errors.Join(importErr, finishErr)
			} else {
				importErr = finishErr
			}
		}
	}()
	skip := 0
	firstPage := true
	for {
		if !firstPage {
			if err := c.wait(ctx); err != nil {
				return ingestion.Stats(), err
			}
		}
		firstPage = false
		page, err := c.fetchPage(ctx, skip, startsAtMin, startsAtMax)
		if err != nil {
			return ingestion.Stats(), fmt.Errorf("fetch timepad page: %w", err)
		}
		ingestion.AddPage()
		ingestion.AddFetched(len(page.Values))
		for _, event := range page.Values {
			if !isMoscowCity(event.Location.City) {
				ingestion.AddRejection(providers.RejectionCity, 1)
				continue
			}
			ingestion.AddMatched(1)
			normalized, rejection := normalizeEventWithReason(event)
			if rejection != "" {
				ingestion.AddRejection(rejection, 1)
				continue
			}
			if normalized.StartsAt.Before(startsAtMin) {
				ingestion.AddRejection(providers.RejectionBeforeWindow, 1)
				continue
			}
			if normalized.StartsAt.After(startsAtMax) {
				ingestion.AddRejection(providers.RejectionAfterWindow, 1)
				continue
			}
			ingestion.AddInsideWindow()
			normalized.ProviderLastSeenRunID = &runID
			if err := ingestion.Persist(ctx, normalized); err != nil {
				return ingestion.Stats(), err
			}
		}
		if maxPages > 0 && ingestion.Stats().PagesFetched >= maxPages {
			return completeTimepadImport(ingestion.Stats(), maxPages)
		}
		if len(page.Values) == 0 || len(page.Values) < c.pageSize {
			return completeTimepadImport(ingestion.Stats(), maxPages)
		}
		skip += len(page.Values)
		if skip >= int(page.Total) {
			return completeTimepadImport(ingestion.Stats(), maxPages)
		}
	}
}

func completeTimepadImport(stats ImportStats, maxPages int) (ImportStats, error) {
	if maxPages == 0 && stats.Matched > 0 && stats.Normalized == 0 {
		return stats, normalizationEmptyError{}
	}
	return stats, nil
}

// importPosterStore tries redirect poster recovery only after the normal upsert
// confirms that the event is new. Timepad imports revisit the full horizon on
// every run, so repeat syncs do not recheck existing image-less rows.
type importPosterStore struct {
	client               *Client
	store                providers.EventStore
	reportError          func(error)
	postersByCanonicalID map[int64][]providers.NormalizedImage
	resolveCanonicalID   func(context.Context, int64, string) (int64, error)
	fetchEventPoster     func(context.Context, int64) ([]providers.NormalizedImage, error)
}

func (s *importPosterStore) UpsertWithResult(ctx context.Context, cityID uuid.UUID, event providers.NormalizedEvent) (providers.UpsertResult, error) {
	result, err := s.store.UpsertWithResult(ctx, cityID, event)
	if err != nil || !result.Inserted || !needsRedirectPosterLookup(event) {
		return result, err
	}
	eventID, parseErr := strconv.ParseInt(event.ExternalID, 10, 64)
	if parseErr != nil || eventID <= 0 {
		s.report(fmt.Errorf("resolve timepad poster for event %q: invalid event ID", event.ExternalID))
		return result, nil
	}
	if err := s.client.wait(ctx); err != nil {
		return result, err
	}
	canonicalID, resolveErr := s.resolveCanonicalID(ctx, eventID, *event.TicketURL)
	if resolveErr != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		s.report(fmt.Errorf("resolve timepad poster for event %q: %w", event.ExternalID, resolveErr))
		return result, nil
	}
	if canonicalID == 0 {
		return result, nil
	}
	images, cached := s.postersByCanonicalID[canonicalID]
	if !cached {
		if err := s.client.wait(ctx); err != nil {
			return result, err
		}
		images, err = s.fetchEventPoster(ctx, canonicalID)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			s.report(fmt.Errorf("fetch timepad poster for event %q: %w", event.ExternalID, err))
			return result, nil
		}
		s.postersByCanonicalID[canonicalID] = images
	}
	if len(images) == 0 {
		return result, nil
	}
	event.Images = images
	if _, err := s.store.UpsertWithResult(ctx, cityID, event); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		s.report(fmt.Errorf("persist recovered timepad poster for event %q: %w", event.ExternalID, err))
	}
	return result, nil
}

func (s *importPosterStore) report(err error) {
	if s.reportError != nil {
		s.reportError(err)
	}
}

func needsRedirectPosterLookup(event providers.NormalizedEvent) bool {
	return len(event.Images) == 0 && event.TicketURL != nil && strings.TrimSpace(*event.TicketURL) != ""
}

func isMoscowCity(city string) bool {
	return strings.EqualFold(strings.TrimSpace(city), moscowCity)
}
