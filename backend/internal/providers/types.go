// Package providers defines provider-neutral event ingestion values.
package providers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	EventStatusPublished = "published"
	EventStatusSoldOut   = "sold_out"
	EventStatusCancelled = "cancelled"
)

type NormalizedEvent struct {
	Source          string
	ExternalID      string
	SourceUpdatedAt *time.Time
	IsDemo          bool
	Title           string
	Subtitle        *string
	Description     string
	Venue           NormalizedVenue
	StartsAt        time.Time
	EndsAt          *time.Time
	Timezone        string
	PriceFromMinor  *int32
	PriceToMinor    *int32
	Currency        string
	// TicketURL is an actionable external provider CTA. Depending on the
	// provider it can be a registration URL or the provider's event page.
	TicketURL *string
	// TicketAvailable means the provider CTA is currently usable according to
	// facts exposed by that provider; it is not a universal inventory guarantee.
	TicketAvailable bool
	Status          string
	AgeRating       *string
	Indoor          *bool
	LoudnessLevel   *string
	PublishedAt     *time.Time
	Categories      []NormalizedCategory
	Images          []NormalizedImage
}

type NormalizedVenue struct {
	ExternalID string
	Name       string
	Address    string
	Latitude   *float64
	Longitude  *float64
	Metro      *string
	VenueType  string
}

type NormalizedCategory struct {
	Slug      string
	Weight    float32
	IsPrimary bool
}

type NormalizedImage struct {
	URL      string
	Width    *int32
	Height   *int32
	Role     string
	Position int32
}

type EventStore interface {
	UpsertWithResult(context.Context, uuid.UUID, NormalizedEvent) (UpsertResult, error)
}

type ImportStats struct {
	PagesFetched int
	Fetched      int
	Matched      int
	Normalized   int
	Inserted     int
	Updated      int
	Skipped      int
	Errors       int
}

// Ingestion owns the provider-neutral duplicate detection and persistence
// accounting shared by external event importers.
type Ingestion struct {
	cityID      uuid.UUID
	store       EventStore
	reportError func(error)
	seen        map[string]struct{}
	stats       ImportStats
}

func NewIngestion(cityID uuid.UUID, store EventStore, reportError func(error)) (*Ingestion, error) {
	if cityID == uuid.Nil {
		return nil, errors.New("import city is required")
	}
	if store == nil {
		return nil, errors.New("provider event store is required")
	}
	return &Ingestion{
		cityID:      cityID,
		store:       store,
		reportError: reportError,
		seen:        make(map[string]struct{}),
	}, nil
}

func (i *Ingestion) AddFetched(count int) {
	i.stats.Fetched += count
}

func (i *Ingestion) AddPage() {
	i.stats.PagesFetched++
}

func (i *Ingestion) AddMatched(count int) {
	i.stats.Matched += count
}

func (i *Ingestion) AddSkipped(count int) {
	i.stats.Skipped += count
}

// Persist records one normalized occurrence. Individual persistence failures
// are reported and counted without aborting the import; context cancellation
// remains fatal so long-running sync can shut down promptly.
func (i *Ingestion) Persist(ctx context.Context, event NormalizedEvent) error {
	key := event.Source + "\x00" + event.ExternalID
	if _, duplicate := i.seen[key]; duplicate {
		i.stats.Skipped++
		return nil
	}
	i.seen[key] = struct{}{}
	i.stats.Normalized++
	result, err := i.store.UpsertWithResult(ctx, i.cityID, event)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		i.stats.Errors++
		if i.reportError != nil {
			i.reportError(fmt.Errorf("persist %s occurrence %s: %w", event.Source, event.ExternalID, err))
		}
		return nil
	}
	if result.Inserted {
		i.stats.Inserted++
	} else {
		i.stats.Updated++
	}
	return nil
}

func (i *Ingestion) Stats() ImportStats {
	return i.stats
}
