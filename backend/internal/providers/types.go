// Package providers defines provider-neutral event ingestion values.
package providers

import "time"

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
	TicketURL       *string
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
	Latitude   float64
	Longitude  float64
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
