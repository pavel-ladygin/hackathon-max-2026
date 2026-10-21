package kudago

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type eventsPage struct {
	Count   int        `json:"count"`
	Next    string     `json:"next"`
	Results []eventDTO `json:"results"`
}

// Missing pagination fields must never look like a complete empty catalog.
func (p *eventsPage) UnmarshalJSON(data []byte) error {
	type plain eventsPage
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"count", "results"} {
		raw, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return providers.SafeError("provider_pagination_error")
		}
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Count < 0 {
		return providers.SafeError("provider_pagination_error")
	}
	*p = eventsPage(value)
	return nil
}

type eventDTO struct {
	ID              int64             `json:"id"`
	PublicationDate int64             `json:"publication_date"`
	Title           string            `json:"title"`
	ShortTitle      string            `json:"short_title"`
	Tagline         string            `json:"tagline"`
	Description     string            `json:"description"`
	BodyText        string            `json:"body_text"`
	Dates           []eventDate       `json:"dates"`
	Categories      []string          `json:"categories"`
	AgeRestriction  ageRestrictionDTO `json:"age_restriction"`
	Price           string            `json:"price"`
	IsFree          bool              `json:"is_free"`
	Images          []eventImage      `json:"images"`
	SiteURL         string            `json:"site_url"`
	Location        locationDTO       `json:"location"`
	Place           *placeDTO         `json:"place"`
}

type eventDate struct {
	Start int64  `json:"start"`
	End   *int64 `json:"end"`
}

type eventImage struct {
	Image string `json:"image"`
}

type locationDTO struct {
	Slug string `json:"slug"`
}

type placeDTO struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	Address    string    `json:"address"`
	Coords     coordsDTO `json:"coords"`
	Subway     string    `json:"subway"`
	Categories []string  `json:"categories"`
}

type coordsDTO struct {
	Lat *float64 `json:"lat"`
	Lon *float64 `json:"lon"`
}

// KudaGo has returned age_restriction as both a JSON string and a number.
// Keep that provider inconsistency inside the private transport DTO.
type ageRestrictionDTO string

func (a *ageRestrictionDTO) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode kudago age restriction: %w", err)
	}
	switch value := value.(type) {
	case nil:
		*a = ""
	case string:
		*a = ageRestrictionDTO(value)
	case json.Number:
		*a = ageRestrictionDTO(value.String())
	default:
		return fmt.Errorf("decode kudago age restriction: unexpected JSON type")
	}
	return nil
}
