package kudago

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

const (
	importHorizon = 90 * 24 * time.Hour
	hardMaxPages  = 1000
)

type EventStore = providers.EventStore
type ImportStats = providers.ImportStats

// Import fetches every KudaGo page and persists valid normalized occurrences.
// A single invalid provider record or persistence error does not stop the run.
// Fetch failures are fatal because the remaining page set is then unknown.
func (c *Client) Import(ctx context.Context, cityID uuid.UUID, store providers.SyncStore, reportError func(error)) (stats ImportStats, importErr error) {
	if c == nil {
		return ImportStats{}, errors.New("kudago client is required")
	}
	return c.ImportPages(ctx, cityID, store, reportError, c.maxPages)
}

// ImportPages is a bounded diagnostic entry point. A positive page cap always
// produces an upsert-only run, even when the provider naturally ends early.
func (c *Client) ImportPages(ctx context.Context, cityID uuid.UUID, store providers.SyncStore, reportError func(error), maxPages int) (stats ImportStats, importErr error) {
	if c == nil {
		return ImportStats{}, errors.New("kudago client is required")
	}
	if cityID == uuid.Nil {
		return ImportStats{}, errors.New("import city is required")
	}
	if maxPages < 0 {
		return ImportStats{}, errors.New("kudago max pages must not be negative")
	}
	if c.cityID != uuid.Nil && cityID != c.cityID {
		return ImportStats{}, errors.New("kudago import city does not match configured city")
	}
	if store == nil {
		return ImportStats{}, errors.New("provider event store is required")
	}

	if c.maxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.maxDuration)
		defer cancel()
	}
	ingestion, err := providers.NewIngestion(cityID, store, reportError)
	if err != nil {
		return ImportStats{}, err
	}
	seenCursors := make(map[string]struct{})
	actualSince := c.now().UTC()
	actualUntil := actualSince.Add(importHorizon)
	// A caller cannot expand the configured cap. An otherwise unlimited run
	// still has a hard ceiling, and every bounded run is audit-only.
	effectiveMaxPages := maxPages
	if c.maxPages > 0 && (effectiveMaxPages == 0 || c.maxPages < effectiveMaxPages) {
		effectiveMaxPages = c.maxPages
	}
	if effectiveMaxPages == 0 {
		effectiveMaxPages = hardMaxPages
	}
	configuredPageCap := maxPages > 0 || c.maxPages > 0
	upsertOnly := c.upsertOnly || configuredPageCap
	runID, err := store.BeginSyncRun(ctx, providers.SyncRunStart{
		Provider: kudagoSource, CityID: cityID, WindowStart: actualSince, WindowEnd: actualUntil, UpsertOnly: upsertOnly,
	})
	if err != nil {
		return ImportStats{}, fmt.Errorf("begin kudago sync run: %w", err)
	}
	defer func() {
		stats.SyncRunID = runID
		reconciled, finishErr := providers.FinalizeSyncRun(ctx, store, runID, stats, importErr)
		stats.Reconciled = reconciled
		if finishErr != nil {
			finishErr = fmt.Errorf("finish kudago sync run: %w", finishErr)
			if importErr != nil {
				importErr = errors.Join(importErr, finishErr)
			} else {
				importErr = finishErr
			}
		}
	}()
	cursor := ""
	httpAttempts := 0
	initialTotal := 0
	hasInitialTotal := false
	result := func() ImportStats {
		snapshot := ingestion.Stats()
		snapshot.HTTPAttempts = httpAttempts
		snapshot.InitialTotal = initialTotal
		snapshot.FinalOffset = snapshot.Fetched
		if upsertOnly {
			snapshot.Scope = "limited_upsert_only"
			if c.upsertOnly && !configuredPageCap {
				snapshot.LimitReason = "configured_upsert_only"
			} else {
				snapshot.LimitReason = "page_cap"
			}
		} else {
			snapshot.Scope = "full"
		}
		return snapshot
	}
	seenEventIDs := make(map[int64]struct{})
	for {
		page, attempts, err := c.fetchPageWithAttempts(ctx, fetchOptions{Cursor: cursor, ActualSince: actualSince, ActualUntil: actualUntil})
		httpAttempts += attempts
		if err != nil {
			return result(), fmt.Errorf("fetch kudago page: %w", err)
		}
		if !hasInitialTotal {
			initialTotal = page.Count
			hasInitialTotal = true
		}
		if page.Count != initialTotal || page.Count < 0 {
			return result(), providers.SafeError("provider_pagination_error")
		}
		if len(page.Results) > c.pageSize || ingestion.Stats().Fetched+len(page.Results) > initialTotal {
			return result(), providers.SafeError("provider_pagination_error")
		}
		if page.Count > 0 && len(page.Results) == 0 {
			return result(), providers.SafeError("provider_empty_result")
		}
		ingestion.AddPage()
		ingestion.AddFetched(len(page.Results))
		cityMismatch := false
		for _, event := range page.Results {
			if event.ID <= 0 {
				return result(), providers.SafeError("provider_empty_result")
			}
			if _, duplicate := seenEventIDs[event.ID]; duplicate {
				return result(), providers.SafeError("provider_pagination_error")
			}
			seenEventIDs[event.ID] = struct{}{}
			if !c.matchesLocation(event.Location.Slug) {
				ingestion.AddRejection(providers.RejectionCity, 1)
				cityMismatch = true
				continue
			}
			ingestion.AddMatched(1)
			occurrences := normalizeEventForTimezone(event, actualSince, actualUntil, c.effectiveTimezone())
			if len(occurrences) == 0 {
				ingestion.AddSkipped(1)
				continue
			}
			for _, occurrence := range occurrences {
				occurrence.ProviderLastSeenRunID = &runID
				if err := ingestion.Persist(ctx, occurrence); err != nil {
					return result(), err
				}
			}
		}
		if cityMismatch {
			return result(), providers.SafeError("provider_city_mismatch")
		}
		next := strings.TrimSpace(page.Next)
		if next == "" && ingestion.Stats().Fetched != initialTotal {
			return result(), providers.SafeError("provider_pagination_error")
		}
		if ingestion.Stats().PagesFetched >= effectiveMaxPages && next != "" {
			snapshot := result()
			snapshot.InitialTotal = initialTotal
			if !configuredPageCap {
				return snapshot, providers.SafeError("provider_pagination_error")
			}
			return snapshot, nil
		}

		if next == "" {
			if ingestion.Stats().Fetched != initialTotal {
				return result(), providers.SafeError("provider_pagination_error")
			}
			snapshot := result()
			snapshot.InitialTotal = initialTotal
			if !upsertOnly && (initialTotal == 0 || snapshot.Fetched != initialTotal || snapshot.Normalized == 0 || snapshot.Skipped > snapshot.Rejections.Duplicate) {
				return snapshot, providers.SafeError("provider_empty_result")
			}
			return snapshot, nil
		}
		if _, duplicate := seenCursors[next]; duplicate {
			return result(), providers.SafeError("provider_pagination_error")
		}
		seenCursors[next] = struct{}{}
		cursor = next
	}
}

func (c *Client) effectiveTimezone() string {
	if c == nil || strings.TrimSpace(c.timezone) == "" {
		return moscowZone
	}
	return c.timezone
}

func (c *Client) matchesLocation(slug string) bool {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return c.cityID == uuid.Nil || strings.EqualFold(c.location, "msk")
	}
	return strings.EqualFold(slug, c.location)
}
