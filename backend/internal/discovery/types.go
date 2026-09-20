// Package discovery provides read-only event discovery data for future HTTP handlers.
package discovery

import (
	"time"

	"github.com/google/uuid"
)

const (
	defaultLimit = 20
	maxLimit     = 50
)

// SearchFilter is the normalized input accepted by the discovery service.
// Nil numeric fields are deliberately distinct from zero-valued filters.
type SearchFilter struct {
	UserID         uuid.UUID
	CityID         uuid.UUID
	Query          *string
	DateFrom       *time.Time
	DateTo         *time.Time
	DayTypes       []string
	TimeSlots      []string
	CategorySlugs  []string
	PriceMaxMinor  *int32
	FreeOnly       bool
	Location       *Location
	DistanceMeters *int32
	Limit          int
	Cursor         *Cursor
}

type Location struct {
	Latitude  float64
	Longitude float64
}

// Cursor contains the exclusive key of the last item in a page. FilterHash
// prevents a cursor from being reused with a different result set.
type Cursor struct {
	StartsAt   time.Time
	EventID    uuid.UUID
	FilterHash string
}

type Card struct {
	ID             uuid.UUID
	Title          string
	Subtitle       *string
	CategorySlug   string
	StartsAt       time.Time
	Timezone       string
	DateLabel      string
	VenueName      string
	DistanceMeters *int
	DistanceLabel  *string
	PriceFromMinor *int
	Currency       string
	PriceLabel     string
	ImageURL       *string
	Saved          bool
	Reasons        []Reason
}

type Reason struct {
	Code string
	Text string
}

type Venue struct {
	ID        uuid.UUID
	Name      string
	Address   string
	Latitude  float64
	Longitude float64
	Metro     *string
	District  *string
}

type Image struct {
	URL    string
	Width  *int
	Height *int
	Role   string
}

type Provenance struct {
	Source          string
	SourceUpdatedAt *time.Time
	IsDemo          bool
}

type Detail struct {
	Card
	Description     string
	EndsAt          *time.Time
	Venue           Venue
	Images          []Image
	TicketAvailable bool
	Status          string
	AgeRating       *string
	Provenance      Provenance
}

type Page struct {
	Items      []Card
	Total      int
	NextCursor *Cursor
}
