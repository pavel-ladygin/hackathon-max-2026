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

type EventStore interface {
	UpsertWithResult(context.Context, uuid.UUID, providers.NormalizedEvent) (providers.UpsertResult, error)
}

type ImportStats struct {
	Fetched    int
	Normalized int
	Inserted   int
	Updated    int
	Skipped    int
	Errors     int
}

// Import fetches every KudaGo page and persists valid normalized occurrences.
// A single invalid provider record or persistence error does not stop the run.
// Fetch failures are fatal because the remaining page set is then unknown.
func (c *Client) Import(ctx context.Context, cityID uuid.UUID, store EventStore, reportError func(error)) (ImportStats, error) {
	if c == nil {
		return ImportStats{}, errors.New("kudago client is required")
	}
	if cityID == uuid.Nil {
		return ImportStats{}, errors.New("import city is required")
	}
	if store == nil {
		return ImportStats{}, errors.New("provider event store is required")
	}

	var stats ImportStats
	seenOccurrences := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	actualSince := c.now().UTC()
	actualUntil := actualSince.Add(importHorizon)
	cursor := ""
	for {
		page, err := c.fetchPage(ctx, fetchOptions{Cursor: cursor, ActualSince: actualSince, ActualUntil: actualUntil})
		if err != nil {
			return stats, fmt.Errorf("fetch kudago page: %w", err)
		}
		stats.Fetched += len(page.Results)
		for _, event := range page.Results {
			occurrences := normalizeEvent(event)
			if len(occurrences) == 0 {
				stats.Skipped++
				continue
			}
			for _, occurrence := range occurrences {
				key := occurrence.Source + "\x00" + occurrence.ExternalID
				if _, duplicate := seenOccurrences[key]; duplicate {
					stats.Skipped++
					continue
				}
				seenOccurrences[key] = struct{}{}
				stats.Normalized++
				result, err := store.UpsertWithResult(ctx, cityID, occurrence)
				if err != nil {
					if ctx.Err() != nil {
						return stats, ctx.Err()
					}
					stats.Errors++
					if reportError != nil {
						reportError(fmt.Errorf("persist kudago occurrence %s: %w", occurrence.ExternalID, err))
					}
					continue
				}
				if result.Inserted {
					stats.Inserted++
				} else {
					stats.Updated++
				}
			}
		}

		next := strings.TrimSpace(page.Next)
		if next == "" {
			return stats, nil
		}
		if _, duplicate := seenCursors[next]; duplicate {
			return stats, errors.New("kudago pagination cursor repeated")
		}
		seenCursors[next] = struct{}{}
		cursor = next
	}
}
