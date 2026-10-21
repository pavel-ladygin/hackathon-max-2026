package main

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventsources"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
)

const genericPriority = 25

type genericSourceImporter struct {
	runner   *eventsources.Runner
	sourceID uuid.UUID
}

func (i genericSourceImporter) Import(ctx context.Context, _ uuid.UUID, _ providers.SyncStore, _ func(error)) (providers.ImportStats, error) {
	return i.runner.Run(ctx, i.sourceID)
}

func genericSyncProviders(sources []sourceconfig.Source, runner *eventsources.Runner, targetCity ...uuid.UUID) []syncProvider {
	ordered := append([]sourceconfig.Source(nil), sources...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].SourceKey == ordered[j].SourceKey {
			return ordered[i].ID.String() < ordered[j].ID.String()
		}
		return ordered[i].SourceKey < ordered[j].SourceKey
	})
	result := make([]syncProvider, 0, len(ordered))
	for _, source := range ordered {
		if !source.Enabled || !strings.HasPrefix(strings.TrimSpace(source.SourceKey), "generic:") {
			continue
		}
		if len(targetCity) > 0 && (runner == nil || runner.CityForSource(source.SourceKey) != targetCity[0]) {
			continue
		}
		result = append(result, syncProvider{name: source.SourceKey, priority: genericPriority, importer: genericSourceImporter{runner: runner, sourceID: source.ID}})
	}
	return result
}
