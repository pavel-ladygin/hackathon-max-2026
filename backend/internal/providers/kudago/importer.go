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

const importHorizon = 90 * 24 * time.Hour

type EventStore = providers.EventStore
type ImportStats = providers.ImportStats

// Import fetches every KudaGo page and persists valid normalized occurrences.
// A single invalid provider record or persistence error does not stop the run.
// Fetch failures are fatal because the remaining page set is then unknown.
func (c *Client) Import(ctx context.Context, cityID uuid.UUID, store providers.SyncStore, reportError func(error)) (stats ImportStats, importErr error) {
	if c == nil {
		return ImportStats{}, errors.New("kudago client is required")
	}
	if cityID == uuid.Nil {
		return ImportStats{}, errors.New("import city is required")
	}
	if store == nil {
		return ImportStats{}, errors.New("provider event store is required")
	}

	ingestion, err := providers.NewIngestion(cityID, store, reportError)
	if err != nil {
		return ImportStats{}, err
	}
	seenCursors := make(map[string]struct{})
	actualSince := c.now().UTC()
	actualUntil := actualSince.Add(importHorizon)
	runID, err := store.BeginSyncRun(ctx, providers.SyncRunStart{
		Provider: kudagoSource, CityID: cityID, WindowStart: actualSince, WindowEnd: actualUntil,
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
	for {
		page, err := c.fetchPage(ctx, fetchOptions{Cursor: cursor, ActualSince: actualSince, ActualUntil: actualUntil})
		if err != nil {
			return ingestion.Stats(), fmt.Errorf("fetch kudago page: %w", err)
		}
		ingestion.AddPage()
		ingestion.AddFetched(len(page.Results))
		ingestion.AddMatched(len(page.Results))
		for _, event := range page.Results {
			occurrences := normalizeEvent(event, actualSince, actualUntil)
			if len(occurrences) == 0 {
				ingestion.AddSkipped(1)
				continue
			}
			for _, occurrence := range occurrences {
				occurrence.ProviderLastSeenRunID = &runID
				if err := ingestion.Persist(ctx, occurrence); err != nil {
					return ingestion.Stats(), err
				}
			}
		}

		next := strings.TrimSpace(page.Next)
		if next == "" {
			return ingestion.Stats(), nil
		}
		if _, duplicate := seenCursors[next]; duplicate {
			return ingestion.Stats(), errors.New("kudago pagination cursor repeated")
		}
		seenCursors[next] = struct{}{}
		cursor = next
	}
}
