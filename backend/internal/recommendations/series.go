package recommendations

import (
	"strings"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
)

type eventSeries struct {
	Event            catalog.Event
	OtherOccurrences int
}

func eventSeriesKey(event catalog.Event) string {
	source := strings.TrimSpace(event.Source)
	externalID := strings.TrimSpace(event.ExternalID)

	// Events without a stable provider identity must never be collapsed.
	// This also preserves demo/test/catalog events that predate provider IDs.
	if source == "" || externalID == "" {
		return "event:" + event.ID.String()
	}

	if source == "kudago" {
		if providerEventID, _, ok := strings.Cut(externalID, ":"); ok && providerEventID != "" {
			return "kudago:" + providerEventID
		}
	}

	return source + ":" + externalID
}

func collapseEventSeries(events []catalog.Event) []eventSeries {
	index := make(map[string]int, len(events))
	result := make([]eventSeries, 0, len(events))

	for _, event := range events {
		key := eventSeriesKey(event)

		if i, exists := index[key]; exists {
			result[i].OtherOccurrences++

			if event.StartsAt.Time.Before(result[i].Event.StartsAt.Time) {
				result[i].Event = event
			}
			continue
		}

		index[key] = len(result)
		result = append(result, eventSeries{Event: event})
	}

	return result
}
