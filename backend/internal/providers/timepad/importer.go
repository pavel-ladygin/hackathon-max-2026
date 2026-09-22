package timepad

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type EventStore = providers.EventStore
type ImportStats = providers.ImportStats

// Import fetches every Timepad page in the configured horizon and persists
// valid normalized events through the shared provider ingestion path.
func (c *Client) Import(ctx context.Context, cityID uuid.UUID, store EventStore, reportError func(error)) (ImportStats, error) {
	if c == nil {
		return ImportStats{}, errors.New("timepad client is required")
	}
	ingestion, err := providers.NewIngestion(cityID, store, reportError)
	if err != nil {
		return ImportStats{}, err
	}
	startsAtMin := c.now().UTC()
	startsAtMax := startsAtMin.Add(importHorizon)
	skip := 0
	for {
		page, err := c.fetchPage(ctx, skip, startsAtMin, startsAtMax)
		if err != nil {
			return ingestion.Stats(), fmt.Errorf("fetch timepad page: %w", err)
		}
		ingestion.AddFetched(len(page.Values))
		for _, event := range page.Values {
			normalized, ok := normalizeEvent(event)
			if !ok {
				ingestion.AddSkipped(1)
				continue
			}
			if err := ingestion.Persist(ctx, normalized); err != nil {
				return ingestion.Stats(), err
			}
		}
		if len(page.Values) == 0 {
			return ingestion.Stats(), nil
		}
		skip += len(page.Values)
		if skip >= int(page.Total) {
			return ingestion.Stats(), nil
		}
	}
}
