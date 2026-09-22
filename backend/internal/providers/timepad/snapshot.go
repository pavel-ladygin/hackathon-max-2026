package timepad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type snapshotPage struct {
	Values []json.RawMessage `json:"values"`
}

// ImportSnapshotFiles imports Timepad API page snapshots in the supplied
// order. Files are opened and decoded one at a time; malformed event records
// are reported and skipped without aborting later records or pages.
func ImportSnapshotFiles(ctx context.Context, paths []string, cityID uuid.UUID, store EventStore, reportError func(error)) (ImportStats, error) {
	ingestion, err := providers.NewIngestion(cityID, store, reportError)
	if err != nil {
		return ImportStats{}, err
	}
	decodeErrors := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return snapshotStats(ingestion, decodeErrors), err
		}
		file, err := os.Open(path)
		if err != nil {
			return snapshotStats(ingestion, decodeErrors), fmt.Errorf("open timepad snapshot %q: %w", path, err)
		}
		page, err := decodeSnapshotPage(file)
		closeErr := file.Close()
		if err != nil {
			decodeErrors++
			if reportError != nil {
				reportError(fmt.Errorf("decode timepad snapshot %q: %w", path, err))
			}
			continue
		}
		if closeErr != nil {
			return snapshotStats(ingestion, decodeErrors), fmt.Errorf("close timepad snapshot %q: %w", path, closeErr)
		}

		ingestion.AddPage()
		for index, raw := range page.Values {
			if err := ctx.Err(); err != nil {
				return snapshotStats(ingestion, decodeErrors), err
			}
			ingestion.AddFetched(1)
			var event eventDTO
			if err := json.Unmarshal(raw, &event); err != nil {
				decodeErrors++
				ingestion.AddSkipped(1)
				if reportError != nil {
					reportError(fmt.Errorf("decode timepad snapshot event %q[%d]: %w", path, index, err))
				}
				continue
			}
			if !isMoscowCity(event.Location.City) {
				ingestion.AddSkipped(1)
				continue
			}
			ingestion.AddMatched(1)
			normalized, ok := normalizeEvent(event)
			if !ok {
				ingestion.AddSkipped(1)
				continue
			}
			if err := ingestion.Persist(ctx, normalized); err != nil {
				return snapshotStats(ingestion, decodeErrors), err
			}
		}
	}
	return snapshotStats(ingestion, decodeErrors), nil
}

func decodeSnapshotPage(reader io.Reader) (snapshotPage, error) {
	decoder := json.NewDecoder(reader)
	var page snapshotPage
	if err := decoder.Decode(&page); err != nil {
		return snapshotPage{}, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return snapshotPage{}, err
	}
	return page, nil
}

func snapshotStats(ingestion *providers.Ingestion, decodeErrors int) ImportStats {
	stats := ingestion.Stats()
	stats.Errors += decodeErrors
	return stats
}
