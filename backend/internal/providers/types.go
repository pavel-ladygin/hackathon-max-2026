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
	// Status contains only a provider-confirmed event lifecycle state. Access
	// restrictions and registration availability must not be encoded as a
	// cancellation or sold-out lifecycle state.
	Status string
	// ProviderActive controls whether the provider record is eligible for the
	// current product flow. Persistence and read-side enforcement are added with
	// provider reconciliation; keeping it distinct here prevents lifecycle lies.
	ProviderActive bool
	// ProviderLastSeenRunID is set only by complete live-provider imports.
	// Snapshot imports leave it nil and therefore never participate in
	// reconciliation.
	ProviderLastSeenRunID *uuid.UUID
	AgeRating             *string
	Indoor                *bool
	LoudnessLevel         *string
	PublishedAt           *time.Time
	Categories            []NormalizedCategory
	Images                []NormalizedImage
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

type SyncRunState string

const (
	SyncRunSucceeded SyncRunState = "succeeded"
	SyncRunFailed    SyncRunState = "failed"
	SyncRunCancelled SyncRunState = "cancelled"
)

type SyncRunStart struct {
	Provider    string
	CityID      uuid.UUID
	WindowStart time.Time
	WindowEnd   time.Time
}

type SyncRunFinish struct {
	RunID     uuid.UUID
	State     SyncRunState
	Stats     ImportStats
	ErrorText string
}

// SyncStore is required by live imports. Snapshot imports intentionally use
// only EventStore and cannot start or reconcile a complete provider run.
type SyncStore interface {
	EventStore
	BeginSyncRun(context.Context, SyncRunStart) (uuid.UUID, error)
	FinishSyncRun(context.Context, SyncRunFinish) (int, error)
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
	Reconciled   int
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

// FinalizeSyncRun records a terminal live-import state using a context that
// survives caller cancellation long enough to persist the audit record.
func FinalizeSyncRun(ctx context.Context, store SyncStore, runID uuid.UUID, stats ImportStats, importErr error) (int, error) {
	state := SyncRunSucceeded
	errorText := ""
	switch {
	case errors.Is(importErr, context.Canceled):
		state = SyncRunCancelled
		errorText = "provider import cancelled"
	case importErr != nil:
		state = SyncRunFailed
		errorText = "provider import failed"
	case stats.Errors > 0:
		state = SyncRunFailed
		errorText = "provider records failed to persist"
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return store.FinishSyncRun(finishCtx, SyncRunFinish{
		RunID: runID, State: state, Stats: stats, ErrorText: errorText,
	})
}
