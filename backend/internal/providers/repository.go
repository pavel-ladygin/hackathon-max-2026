package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

var providerIdentityNamespace = uuid.MustParse("72376b62-fce0-4da3-87e5-e9eff2ad5751")

type Repository struct {
	db *store.Pool
}

type UpsertResult struct {
	EventID  uuid.UUID
	Inserted bool
}

func NewRepository(db *store.Pool) *Repository {
	return &Repository{db: db}
}

// Upsert atomically reconciles one normalized provider occurrence and its
// venue, categories, and images. Provider records can never overwrite demo
// identities through this write path.
func (r *Repository) Upsert(ctx context.Context, cityID uuid.UUID, event NormalizedEvent) (eventID uuid.UUID, err error) {
	result, err := r.UpsertWithResult(ctx, cityID, event)
	return result.EventID, err
}

// UpsertWithResult behaves like Upsert and also reports whether the event row
// was inserted or updated. The distinction is produced by the same atomic
// PostgreSQL upsert used to reconcile the occurrence.
func (r *Repository) UpsertWithResult(ctx context.Context, cityID uuid.UUID, event NormalizedEvent) (result UpsertResult, err error) {
	if r == nil || r.db == nil {
		return UpsertResult{}, errors.New("provider repository database is required")
	}
	if err := validatePersistenceInput(cityID, event); err != nil {
		return UpsertResult{}, err
	}

	identity := strings.TrimSpace(event.Source) + "\x00" + strings.TrimSpace(event.ExternalID)
	candidateEventID := providerStableID("event", identity)
	venueIdentity := strings.TrimSpace(event.Venue.ExternalID)
	if venueIdentity == "" {
		venueIdentity = "event:" + strings.TrimSpace(event.ExternalID)
	}
	venueID := providerStableID("venue", strings.TrimSpace(event.Source)+"\x00"+venueIdentity)

	err = r.db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := platform.New(tx)
		venueType := strings.TrimSpace(event.Venue.VenueType)
		if venueType == "" {
			venueType = "other"
		}
		if err := queries.UpsertProviderVenue(ctx, platform.UpsertProviderVenueParams{
			ID: venueID, CityID: cityID, Name: event.Venue.Name, Address: event.Venue.Address,
			Latitude: optionalFloat64(event.Venue.Latitude), Longitude: optionalFloat64(event.Venue.Longitude),
			Metro: optionalText(event.Venue.Metro), VenueType: venueType,
		}); err != nil {
			return fmt.Errorf("upsert provider venue: %w", err)
		}

		upserted, err := queries.UpsertProviderEvent(ctx, platform.UpsertProviderEventParams{
			ID: candidateEventID, Source: event.Source, ExternalID: event.ExternalID,
			SourceUpdatedAt: optionalTime(event.SourceUpdatedAt), Title: event.Title,
			Subtitle: optionalText(event.Subtitle), Description: event.Description, VenueID: venueID,
			StartsAt: requiredTime(event.StartsAt), EndsAt: optionalTime(event.EndsAt), Timezone: event.Timezone,
			PriceFromMinor: optionalInt32(event.PriceFromMinor), PriceToMinor: optionalInt32(event.PriceToMinor),
			Currency: event.Currency, TicketUrl: optionalText(event.TicketURL), TicketAvailable: event.TicketAvailable,
			Status: event.Status, AgeRating: optionalText(event.AgeRating), Indoor: optionalBool(event.Indoor),
			LoudnessLevel: optionalText(event.LoudnessLevel), PublishedAt: optionalTime(event.PublishedAt),
			ProviderActive: event.ProviderActive, ProviderLastSeenRunID: optionalUUID(event.ProviderLastSeenRunID),
		})
		if err != nil {
			return fmt.Errorf("upsert provider event: %w", err)
		}
		result = UpsertResult{EventID: upserted.ID, Inserted: upserted.Inserted}

		if err := queries.ClearProviderEventCategories(ctx, result.EventID); err != nil {
			return fmt.Errorf("clear provider event categories: %w", err)
		}
		for _, category := range event.Categories {
			if err := queries.InsertProviderEventCategory(ctx, platform.InsertProviderEventCategoryParams{
				EventID: result.EventID, CategorySlug: category.Slug, Weight: category.Weight, IsPrimary: category.IsPrimary,
			}); err != nil {
				return fmt.Errorf("insert provider event category: %w", err)
			}
		}

		if err := queries.ClearProviderEventImages(ctx, result.EventID); err != nil {
			return fmt.Errorf("clear provider event images: %w", err)
		}
		for _, image := range event.Images {
			imageIdentity := fmt.Sprintf("%s\x00%d\x00%s", identity, image.Position, image.URL)
			if err := queries.InsertProviderEventImage(ctx, platform.InsertProviderEventImageParams{
				ID: providerStableID("image", imageIdentity), EventID: result.EventID, Url: image.URL,
				Width: optionalInt32(image.Width), Height: optionalInt32(image.Height), Role: image.Role, Position: image.Position,
			}); err != nil {
				return fmt.Errorf("insert provider event image: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return UpsertResult{}, fmt.Errorf("persist normalized provider event: %w", err)
	}
	return result, nil
}

func (r *Repository) BeginSyncRun(ctx context.Context, start SyncRunStart) (uuid.UUID, error) {
	if r == nil || r.db == nil {
		return uuid.Nil, errors.New("provider repository database is required")
	}
	provider := strings.TrimSpace(start.Provider)
	switch {
	case provider == "" || provider == "demo":
		return uuid.Nil, errors.New("sync run provider is invalid")
	case start.CityID == uuid.Nil:
		return uuid.Nil, errors.New("sync run city is required")
	case start.WindowStart.IsZero() || start.WindowEnd.Before(start.WindowStart):
		return uuid.Nil, errors.New("sync run window is invalid")
	}
	runID := uuid.New()
	created, err := platform.New(r.db).CreateProviderSyncRun(ctx, platform.CreateProviderSyncRunParams{
		ID: runID, Provider: provider, CityID: start.CityID,
		WindowStart: requiredTime(start.WindowStart), WindowEnd: requiredTime(start.WindowEnd),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create provider sync run: %w", err)
	}
	return created, nil
}

func (r *Repository) FinishSyncRun(ctx context.Context, finish SyncRunFinish) (reconciled int, err error) {
	if r == nil || r.db == nil {
		return 0, errors.New("provider repository database is required")
	}
	if finish.RunID == uuid.Nil {
		return 0, errors.New("sync run ID is required")
	}
	if finish.State != SyncRunSucceeded && finish.State != SyncRunFailed && finish.State != SyncRunCancelled {
		return 0, errors.New("sync run final state is invalid")
	}
	if err := validateImportStats(finish.Stats); err != nil {
		return 0, err
	}
	if finish.State == SyncRunSucceeded && finish.Stats.Errors > 0 {
		finish.State = SyncRunFailed
		finish.ErrorText = "provider records failed to persist"
	}

	err = r.db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		queries := platform.New(tx)
		run, err := queries.GetProviderSyncRunForUpdate(ctx, finish.RunID)
		if err != nil {
			return fmt.Errorf("lock provider sync run: %w", err)
		}
		if run.State != "running" {
			reconciled = int(run.Reconciled)
			return nil
		}

		if finish.State == SyncRunSucceeded {
			newer, err := queries.HasNewerProviderSyncRun(ctx, platform.HasNewerProviderSyncRunParams{
				Provider: run.Provider, CityID: run.CityID, StartedAt: run.StartedAt,
			})
			if err != nil {
				return fmt.Errorf("check newer provider sync run: %w", err)
			}
			if !newer {
				count, err := queries.ReconcileProviderSyncRun(ctx, finish.RunID)
				if err != nil {
					return fmt.Errorf("reconcile provider sync run: %w", err)
				}
				reconciled = int(count)
			}
		}

		stats := finish.Stats
		if err := queries.CompleteProviderSyncRun(ctx, platform.CompleteProviderSyncRunParams{
			ID: finish.RunID, State: string(finish.State),
			PagesFetched: int32(stats.PagesFetched), Fetched: int32(stats.Fetched), Matched: int32(stats.Matched),
			Normalized: int32(stats.Normalized), Inserted: int32(stats.Inserted), Updated: int32(stats.Updated),
			Skipped: int32(stats.Skipped), Errors: int32(stats.Errors), Reconciled: int32(reconciled),
			ErrorText: optionalTextValue(safeSyncErrorText(finish.ErrorText)),
		}); err != nil {
			return fmt.Errorf("complete provider sync run: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return reconciled, nil
}

func validateImportStats(stats ImportStats) error {
	values := []int{stats.PagesFetched, stats.Fetched, stats.Matched, stats.Normalized, stats.Inserted, stats.Updated, stats.Skipped, stats.Errors}
	for _, value := range values {
		if value < 0 || int64(value) > int64(^uint32(0)>>1) {
			return errors.New("sync run stats are invalid")
		}
	}
	return nil
}

func safeSyncErrorText(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "", "provider import cancelled", "provider import failed", "provider records failed to persist":
		return value
	default:
		// Sync audit rows intentionally retain only a coarse error class. Raw
		// provider responses and errors may contain tokens or private payloads.
		return "provider sync failed"
	}
}

func validatePersistenceInput(cityID uuid.UUID, event NormalizedEvent) error {
	source := strings.TrimSpace(event.Source)
	switch {
	case cityID == uuid.Nil:
		return errors.New("provider event city is required")
	case source == "" || source == "demo" || event.IsDemo:
		return errors.New("provider event source is invalid")
	case strings.TrimSpace(event.ExternalID) == "":
		return errors.New("provider event external ID is required")
	case strings.TrimSpace(event.Title) == "":
		return errors.New("provider event title is required")
	case strings.TrimSpace(event.Venue.Name) == "":
		return errors.New("provider event venue name is required")
	case event.StartsAt.IsZero():
		return errors.New("provider event start is required")
	case strings.TrimSpace(event.Timezone) == "":
		return errors.New("provider event timezone is required")
	case strings.TrimSpace(event.Currency) == "":
		return errors.New("provider event currency is required")
	case strings.TrimSpace(event.Status) == "":
		return errors.New("provider event status is required")
	case len(event.Categories) == 0:
		return errors.New("provider event category is required")
	}
	return nil
}

func providerStableID(kind, identity string) uuid.UUID {
	return uuid.NewSHA1(providerIdentityNamespace, []byte(kind+"/"+identity))
}

func optionalText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func optionalInt32(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}

func optionalTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return requiredTime(*value)
}

func requiredTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func optionalBool(value *bool) pgtype.Bool {
	if value == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *value, Valid: true}
}

func optionalUUID(value *uuid.UUID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *value, Valid: true}
}

func optionalTextValue(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func optionalFloat64(value *float64) pgtype.Float8 {
	if value == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *value, Valid: true}
}
