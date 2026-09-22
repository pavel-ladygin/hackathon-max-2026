// Package catalog loads the platform catalog as a coherent city snapshot.
package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

// Event is a catalog event with its denormalized category and image data.
type Event struct {
	platform.Event
	Categories []platform.EventCategory
	Images     []platform.EventImage
}

// Snapshot contains all catalog records belonging to one city.
type Snapshot struct {
	City          platform.City
	MetroStations []platform.MetroStation
	Venues        []platform.Venue
	Events        []Event
}

// Repository loads catalog data from the platform database.
type Repository struct {
	db *store.Pool
}

var _ contracts.EventAvailability = (*Repository)(nil)

// NewRepository creates a catalog repository backed by db.
func NewRepository(db *store.Pool) *Repository {
	return &Repository{db: db}
}

// CheckForRoomVote reads the event's current catalog facts directly from
// PostgreSQL. A missing event is represented by Exists=false.
func (r *Repository) CheckForRoomVote(ctx context.Context, eventID uuid.UUID) (contracts.Availability, error) {
	dbtx := store.DBTXFromContext(ctx)
	if dbtx == nil {
		dbtx = r.db
	}
	event, err := platform.New(dbtx).GetEventAvailability(ctx, eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.Availability{}, nil
	}
	if err != nil {
		return contracts.Availability{}, err
	}

	return availabilityFromRow(event), nil
}

func availabilityFromRow(event platform.GetEventAvailabilityRow) contracts.Availability {
	availability := contracts.Availability{
		Exists:          true,
		Status:          event.Status,
		StartsAt:        event.StartsAt.Time,
		Currency:        event.Currency,
		TicketAvailable: event.TicketAvailable,
	}
	if event.PriceFromMinor.Valid {
		price := event.PriceFromMinor.Int32
		availability.PriceFromMinor = &price
	}
	if event.PriceToMinor.Valid {
		price := event.PriceToMinor.Int32
		availability.PriceToMinor = &price
	}
	if event.TicketUrl.Valid {
		url := event.TicketUrl.String
		availability.TicketURL = &url
	}
	return availability
}

// LoadCity reads a city catalog from one repeatable-read, read-only transaction.
// It returns pgx.ErrNoRows when cityID is not present.
func (r *Repository) LoadCity(ctx context.Context, cityID uuid.UUID) (snapshot Snapshot, err error) {
	if dbtx := store.DBTXFromContext(ctx); dbtx != nil {
		return r.loadCity(ctx, dbtx, cityID)
	}

	err = r.db.InTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		snapshot, err = r.loadCity(ctx, tx, cityID)
		return err
	})
	return snapshot, err
}

func (r *Repository) loadCity(ctx context.Context, dbtx store.DBTX, cityID uuid.UUID) (Snapshot, error) {
	queries := platform.New(dbtx)

	city, err := queries.GetCatalogCity(ctx, cityID)
	if err != nil {
		return Snapshot{}, err
	}
	stations, err := queries.ListCatalogMetroStations(ctx, cityID)
	if err != nil {
		return Snapshot{}, err
	}
	venues, err := queries.ListCatalogVenues(ctx, cityID)
	if err != nil {
		return Snapshot{}, err
	}
	events, err := queries.ListCatalogEvents(ctx, cityID)
	if err != nil {
		return Snapshot{}, err
	}
	categories, err := queries.ListCatalogCategories(ctx, cityID)
	if err != nil {
		return Snapshot{}, err
	}
	images, err := queries.ListCatalogImages(ctx, cityID)
	if err != nil {
		return Snapshot{}, err
	}

	byEvent := make(map[uuid.UUID]*Event, len(events))
	snapshot := Snapshot{
		City:          city,
		MetroStations: stations,
		Venues:        venues,
		Events:        make([]Event, len(events)),
	}
	for i, event := range events {
		snapshot.Events[i].Event = event
		byEvent[event.ID] = &snapshot.Events[i]
	}
	for _, category := range categories {
		if event := byEvent[category.EventID]; event != nil {
			event.Categories = append(event.Categories, category)
		}
	}
	for _, image := range images {
		if event := byEvent[image.EventID]; event != nil {
			event.Images = append(event.Images, image)
		}
	}
	return snapshot, nil
}
