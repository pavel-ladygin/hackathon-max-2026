package discovery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

// Repository executes generated, read-only discovery projections.
type Repository struct{ db *store.Pool }

func NewRepository(db *store.Pool) *Repository { return &Repository{db: db} }

// GetActiveRoom returns the caller's unexpired non-terminal room, if one
// exists. It never mutates stale memberships; room lifecycle owns retirement.
func (r *Repository) GetActiveRoom(ctx context.Context, userID uuid.UUID) (ActiveRoom, bool, error) {
	if userID == uuid.Nil {
		return ActiveRoom{}, false, ErrInvalidFilter
	}
	row, err := platform.New(r.db).GetDiscoveryActiveRoom(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ActiveRoom{}, false, nil
	}
	if err != nil {
		return ActiveRoom{}, false, err
	}
	return ActiveRoom{ID: row.ID, Name: row.Name, CityID: row.CityID, State: row.State}, true, nil
}

// UserCity returns the profile city used when discovery has no explicit city.
func (r *Repository) UserCity(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	if userID == uuid.Nil {
		return uuid.Nil, ErrInvalidFilter
	}
	city, err := platform.New(r.db).GetDiscoveryUserCity(ctx, userID)
	if err != nil {
		return uuid.Nil, err
	}
	if !city.Valid {
		return uuid.Nil, pgx.ErrNoRows
	}
	return uuid.UUID(city.Bytes), nil
}

func (r *Repository) Search(ctx context.Context, filter SearchFilter) (page Page, err error) {
	err = r.db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		queries := platform.New(tx)
		rows, err := queries.SearchDiscoveryEventCards(ctx, searchParams(filter, filter.Limit+1))
		if err != nil {
			return err
		}
		page.Items = make([]Card, 0, len(rows))
		for _, row := range rows {
			page.Items = append(page.Items, cardFromGenerated(row))
		}
		if len(page.Items) > filter.Limit {
			last := page.Items[filter.Limit-1]
			page.NextCursor = &Cursor{StartsAt: last.StartsAt, EventID: last.ID}
			page.Items = page.Items[:filter.Limit]
		}
		total, err := queries.CountDiscoveryEventCards(ctx, countParams(filter))
		if err != nil {
			return err
		}
		page.Total = int(total)
		return nil
	})
	return page, err
}

// SearchMapPage reads one map-search page without calculating the catalog-wide
// total. Map responses derive exact counts from all pages as they are consumed.
func (r *Repository) SearchMapPage(ctx context.Context, filter SearchFilter) (page Page, err error) {
	err = r.db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		rows, err := platform.New(tx).SearchDiscoveryEventCards(ctx, searchParams(filter, filter.Limit+1))
		if err != nil {
			return err
		}
		page.Items = make([]Card, 0, min(len(rows), filter.Limit))
		for _, row := range rows {
			page.Items = append(page.Items, cardFromGenerated(row))
		}
		if len(page.Items) > filter.Limit {
			last := page.Items[filter.Limit-1]
			page.NextCursor = &Cursor{StartsAt: last.StartsAt, EventID: last.ID}
			page.Items = page.Items[:filter.Limit]
		}
		return nil
	})
	return page, err
}

func (r *Repository) Get(ctx context.Context, userID, eventID uuid.UUID, location *Location) (detail Detail, err error) {
	err = r.db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		queries := platform.New(tx)
		row, err := queries.GetDiscoveryEventDetail(ctx, detailParams(userID, eventID, location))
		if err != nil {
			return err
		}
		detail = detailFromGenerated(row)
		images, err := queries.ListDiscoveryEventImages(ctx, eventID)
		if err != nil {
			return err
		}
		detail.Images = make([]Image, 0, len(images))
		for _, image := range images {
			detail.Images = append(detail.Images, imageFromGenerated(image))
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, pgx.ErrNoRows
	}
	return detail, err
}

func searchParams(filter SearchFilter, limit int) platform.SearchDiscoveryEventCardsParams {
	params := platform.SearchDiscoveryEventCardsParams{UserID: filter.UserID, LimitCount: int32(limit), CityID: filter.CityID, Query: optionalText(filter.Query), DateFrom: optionalDate(filter.DateFrom), DateTo: optionalDate(filter.DateTo), DayTypes: filter.DayTypes, TimeSlots: filter.TimeSlots, CategorySlugs: filter.CategorySlugs, PriceMaxMinor: optionalInt4(filter.PriceMaxMinor), FreeOnly: filter.FreeOnly, DistanceMeters: optionalInt4(filter.DistanceMeters)}
	if filter.Bounds != nil {
		params.BoundsWest = pgtype.Float8{Float64: filter.Bounds.West, Valid: true}
		params.BoundsSouth = pgtype.Float8{Float64: filter.Bounds.South, Valid: true}
		params.BoundsEast = pgtype.Float8{Float64: filter.Bounds.East, Valid: true}
		params.BoundsNorth = pgtype.Float8{Float64: filter.Bounds.North, Valid: true}
	}
	if filter.Cursor != nil {
		params.CursorStartsAt = pgtype.Timestamptz{Time: filter.Cursor.StartsAt, Valid: true}
		params.CursorEventID = pgtype.UUID{Bytes: filter.Cursor.EventID, Valid: true}
	}
	params.Latitude, params.Longitude = locationValues(filter.Location)
	return params
}

func countParams(filter SearchFilter) platform.CountDiscoveryEventCardsParams {
	latitude, longitude := locationValues(filter.Location)
	params := platform.CountDiscoveryEventCardsParams{CityID: filter.CityID, Query: optionalText(filter.Query), DateFrom: optionalDate(filter.DateFrom), DateTo: optionalDate(filter.DateTo), DayTypes: filter.DayTypes, TimeSlots: filter.TimeSlots, CategorySlugs: filter.CategorySlugs, PriceMaxMinor: optionalInt4(filter.PriceMaxMinor), FreeOnly: filter.FreeOnly, DistanceMeters: optionalInt4(filter.DistanceMeters), Latitude: latitude, Longitude: longitude}
	if filter.Bounds != nil {
		params.BoundsWest = pgtype.Float8{Float64: filter.Bounds.West, Valid: true}
		params.BoundsSouth = pgtype.Float8{Float64: filter.Bounds.South, Valid: true}
		params.BoundsEast = pgtype.Float8{Float64: filter.Bounds.East, Valid: true}
		params.BoundsNorth = pgtype.Float8{Float64: filter.Bounds.North, Valid: true}
	}
	return params
}

func detailParams(userID, eventID uuid.UUID, location *Location) platform.GetDiscoveryEventDetailParams {
	latitude, longitude := locationValues(location)
	return platform.GetDiscoveryEventDetailParams{UserID: userID, EventID: eventID, Latitude: latitude, Longitude: longitude}
}

func optionalText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}
func optionalDate(value *time.Time) pgtype.Date {
	if value == nil {
		return pgtype.Date{}
	}
	date, err := time.Parse(time.DateOnly, *dateArgument(value))
	if err != nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: date, Valid: true}
}

// dateArgument preserves the calendar date independently of the time value's
// zone; OpenAPI date filters do not represent instants.
func dateArgument(value *time.Time) *string {
	if value == nil {
		return nil
	}
	date := value.Format(time.DateOnly)
	return &date
}

func optionalInt4(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}
func locationValues(location *Location) (pgtype.Float8, pgtype.Float8) {
	if location == nil {
		return pgtype.Float8{}, pgtype.Float8{}
	}
	return pgtype.Float8{Float64: location.Latitude, Valid: true}, pgtype.Float8{Float64: location.Longitude, Valid: true}
}

func cardFromGenerated(row platform.SearchDiscoveryEventCardsRow) Card {
	card := cardFromFields(row.ID, row.Title, row.Subtitle, row.CategorySlug, row.StartsAt, row.Timezone, row.VenueName, optionalDistance(row.DistanceM), row.PriceFromMinor, row.Currency, row.ImageUrl, row.Saved)
	if row.Latitude.Valid && row.Longitude.Valid {
		card.Latitude = &row.Latitude.Float64
		card.Longitude = &row.Longitude.Float64
	}
	return card
}
func detailFromGenerated(row platform.GetDiscoveryEventDetailRow) Detail {
	card := cardFromFields(row.ID, row.Title, row.Subtitle, row.CategorySlug, row.StartsAt, row.Timezone, row.Name, optionalDistance(row.Column8), row.PriceFromMinor, row.Currency, row.ImageUrl, row.Exists)
	detail := Detail{Card: card, Description: row.Description, Venue: Venue{ID: row.ID_2, Name: row.Name, Address: row.Address}, Images: []Image{}, TicketAvailable: row.TicketAvailable, Status: row.Status, Provenance: Provenance{Source: row.Source, IsDemo: row.IsDemo}}
	if row.Latitude.Valid && row.Longitude.Valid {
		detail.Venue.Latitude = &row.Latitude.Float64
		detail.Venue.Longitude = &row.Longitude.Float64
	}
	if row.EndsAt.Valid {
		detail.EndsAt = timePtr(row.EndsAt.Time)
	}
	if row.Metro.Valid {
		detail.Venue.Metro = stringPtr(row.Metro.String)
	}
	if row.District.Valid {
		detail.Venue.District = stringPtr(row.District.String)
	}
	if row.AgeRating.Valid {
		detail.AgeRating = stringPtr(row.AgeRating.String)
	}
	if row.SourceUpdatedAt.Valid {
		detail.Provenance.SourceUpdatedAt = timePtr(row.SourceUpdatedAt.Time)
	}
	return detail
}
func cardFromFields(id uuid.UUID, title string, subtitle pgtype.Text, category string, startsAt pgtype.Timestamptz, timezone, venue string, distance pgtype.Float8, price pgtype.Int4, currency, image string, saved bool) Card {
	card := Card{ID: id, Title: title, CategorySlug: category, StartsAt: startsAt.Time, Timezone: timezone, DateLabel: dateLabel(startsAt.Time, timezone), VenueName: venue, Currency: currency, PriceLabel: priceLabel(price), Saved: saved, Reasons: []Reason{}}
	if subtitle.Valid {
		card.Subtitle = stringPtr(subtitle.String)
	}
	if image != "" {
		card.ImageURL = stringPtr(image)
	}
	if price.Valid {
		card.PriceFromMinor = intPtr(int(price.Int32))
	}
	if distance.Valid {
		meters := int(math.Round(distance.Float64))
		card.DistanceMeters, card.DistanceLabel = intPtr(meters), stringPtr(distanceLabel(meters))
	}
	return card
}
func optionalDistance(value any) pgtype.Float8 {
	switch value := value.(type) {
	case float64:
		return pgtype.Float8{Float64: value, Valid: true}
	case float32:
		return pgtype.Float8{Float64: float64(value), Valid: true}
	case pgtype.Float8:
		return value
	default:
		return pgtype.Float8{}
	}
}
func imageFromGenerated(row platform.ListDiscoveryEventImagesRow) Image {
	image := Image{URL: row.Url, Role: row.Role}
	if row.Width.Valid {
		image.Width = intPtr(int(row.Width.Int32))
	}
	if row.Height.Valid {
		image.Height = intPtr(int(row.Height.Int32))
	}
	return image
}
func stringPtr(value string) *string     { return &value }
func intPtr(value int) *int              { return &value }
func timePtr(value time.Time) *time.Time { return &value }
func dateLabel(startsAt time.Time, timezone string) string {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return startsAt.UTC().Format("02.01, 15:04")
	}
	local := startsAt.In(location)
	months := [...]string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}
	return fmt.Sprintf("%d %s, %02d:%02d", local.Day(), months[local.Month()-1], local.Hour(), local.Minute())
}
func priceLabel(price pgtype.Int4) string {
	if !price.Valid {
		return "Цена уточняется"
	}
	if price.Int32 == 0 {
		return "Бесплатно"
	}
	rubles, kopecks := price.Int32/100, price.Int32%100
	if kopecks == 0 {
		return fmt.Sprintf("от %s ₽", formatThousands(rubles))
	}
	return fmt.Sprintf("от %s,%02d ₽", formatThousands(rubles), kopecks)
}
func distanceLabel(meters int) string {
	if meters < 1000 {
		return fmt.Sprintf("%d м", meters)
	}
	return fmt.Sprintf("%.1f км", float64(meters)/1000)
}
func formatThousands(value int32) string {
	text := fmt.Sprintf("%d", value)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + " " + text[i:]
	}
	return text
}
