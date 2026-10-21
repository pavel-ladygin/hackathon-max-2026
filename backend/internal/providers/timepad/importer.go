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

type safetyError struct{ code string }

func (e safetyError) Error() string         { return "timepad import safety check failed" }
func (e safetyError) SyncErrorCode() string { return e.code }

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
	if cityID == uuid.Nil {
		return ImportStats{}, errors.New("import city is required")
	}
	if c.cityID != uuid.Nil && cityID != c.cityID {
		return ImportStats{}, safetyError{code: "provider_city_mismatch"}
	}
	if c.maxRunDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.maxRunDuration)
		defer cancel()
	}
	effectiveMaxPages := maxPages
	limitReason := ""
	if c.maxPagesPerRun > 0 && (effectiveMaxPages == 0 || c.maxPagesPerRun < effectiveMaxPages) {
		effectiveMaxPages = c.maxPagesPerRun
		limitReason = "configured_page_cap"
	}
	if maxPages > 0 && effectiveMaxPages == maxPages {
		limitReason = "caller_page_cap"
	}
	if effectiveMaxPages == 0 {
		effectiveMaxPages = hardPageCeiling
	}
	upsertOnly := c.upsertOnly || maxPages > 0 || c.maxPagesPerRun > 0
	attemptStart := c.attempts.Load()
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
	initialTotal := 0
	haveInitialTotal := false
	runID, err := store.BeginSyncRun(ctx, providers.SyncRunStart{
		Provider: timepadSource, CityID: cityID, WindowStart: startsAtMin, WindowEnd: startsAtMax, UpsertOnly: upsertOnly,
	})
	if err != nil {
		return ImportStats{}, providers.SafeError("provider_persistence_error")
	}
	defer func() {
		stats.HTTPAttempts = int(c.attempts.Load() - attemptStart)
		stats.InitialTotal = initialTotal
		stats.FinalOffset = stats.Fetched
		if upsertOnly {
			stats.Scope = "limited_upsert_only"
			if limitReason == "" && c.upsertOnly {
				limitReason = "configured_upsert_only"
			}
			stats.LimitReason = limitReason
		} else {
			stats.Scope = "full"
		}
		stats.SyncRunID = runID
		reconciled, finishErr := providers.FinalizeSyncRun(ctx, store, runID, stats, importErr)
		stats.Reconciled = reconciled
		if finishErr != nil {
			finishErr = providers.SafeError("provider_finalization_error")
			if importErr != nil {
				importErr = errors.Join(importErr, finishErr)
			} else {
				importErr = finishErr
			}
		}
	}()
	skip := 0
	firstPage := true
	seenEventIDs := make(map[int64]struct{})
	for {
		if !firstPage {
			if err := c.wait(ctx); err != nil {
				return ingestion.Stats(), err
			}
		}
		firstPage = false
		page, err := c.fetchPage(ctx, skip, startsAtMin, startsAtMax)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return ingestion.Stats(), safetyError{code: "provider_timeout"}
			}
			return ingestion.Stats(), fmt.Errorf("fetch timepad page: %w", err)
		}
		if !haveInitialTotal {
			initialTotal = int(page.Total)
			haveInitialTotal = true
		} else if int(page.Total) != initialTotal {
			return ingestion.Stats(), safetyError{code: "provider_pagination_error"}
		}
		if len(page.Values) == 0 && skip < int(page.Total) {
			return ingestion.Stats(), safetyError{code: "provider_pagination_error"}
		}
		if len(page.Values) > c.pageSize || skip+len(page.Values) > int(page.Total) {
			return ingestion.Stats(), safetyError{code: "provider_pagination_error"}
		}
		if len(page.Values) < c.pageSize && skip+len(page.Values) < int(page.Total) {
			return ingestion.Stats(), safetyError{code: "provider_pagination_error"}
		}
		ingestion.AddPage()
		ingestion.AddFetched(len(page.Values))
		for _, event := range page.Values {
			if _, duplicate := seenEventIDs[event.ID]; duplicate {
				ingestion.AddRejection(providers.RejectionDuplicate, 1)
				return ingestion.Stats(), safetyError{code: "provider_pagination_error"}
			}
			seenEventIDs[event.ID] = struct{}{}
			if !c.matchesCity(event.Location.City) {
				ingestion.AddRejection(providers.RejectionCity, 1)
				return ingestion.Stats(), safetyError{code: "provider_city_mismatch"}
			}
			ingestion.AddMatched(1)
			normalized, rejection := normalizeEventWithMetadata(event, c.effectiveCityName(), c.effectiveTimezone())
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
		if ingestion.Stats().PagesFetched >= effectiveMaxPages {
			if skip+len(page.Values) < int(page.Total) && effectiveMaxPages == hardPageCeiling && maxPages == 0 && c.maxPagesPerRun == 0 {
				return ingestion.Stats(), safetyError{code: "provider_pagination_error"}
			}
			return completeTimepadImport(ingestion.Stats(), !upsertOnly)
		}
		if len(page.Values) == 0 || len(page.Values) < c.pageSize {
			return completeTimepadImport(ingestion.Stats(), !upsertOnly)
		}
		skip += len(page.Values)
		if skip >= int(page.Total) {
			return completeTimepadImport(ingestion.Stats(), !upsertOnly)
		}
	}
}

func completeTimepadImport(stats ImportStats, fullRun bool) (ImportStats, error) {
	if fullRun && stats.Matched == 0 {
		return stats, safetyError{code: "provider_empty_result"}
	}
	if fullRun && stats.Matched > 0 && stats.Normalized == 0 {
		return stats, normalizationEmptyError{}
	}
	if fullRun && normalizationRejections(stats.Rejections) > 0 {
		return stats, providers.SafeError("provider_decode_error")
	}
	return stats, nil
}

func normalizationRejections(rejections providers.RejectionStats) int {
	return rejections.InvalidID + rejections.MissingTitle + rejections.MissingStartsAt +
		rejections.InvalidStartsAt + rejections.MalformedCategories + rejections.Other
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
		if providers.IsBlockingError(resolveErr) {
			return result, resolveErr
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
			if providers.IsBlockingError(err) {
				return result, err
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
		return result, providers.SafeError("provider_persistence_error")
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
	return matchesCity(city, moscowCity)
}

func (c *Client) effectiveCityName() string {
	if c == nil || strings.TrimSpace(c.cityName) == "" {
		return moscowCity
	}
	return c.cityName
}

func (c *Client) effectiveTimezone() string {
	if c == nil || strings.TrimSpace(c.timezone) == "" {
		return moscowZone
	}
	return c.timezone
}

func (c *Client) matchesCity(city string) bool {
	return matchesCity(city, c.effectiveCityName())
}

func matchesCity(city, configuredCity string) bool {
	return strings.EqualFold(strings.TrimSpace(city), strings.TrimSpace(configuredCity))
}
