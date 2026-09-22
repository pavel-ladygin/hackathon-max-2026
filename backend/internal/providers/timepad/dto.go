package timepad

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

type eventsPage struct {
	Total  flexibleInt `json:"total"`
	Values []eventDTO  `json:"values"`
}

type eventDTO struct {
	ID               int64               `json:"id"`
	CreatedAt        string              `json:"created_at"`
	StartsAt         string              `json:"starts_at"`
	EndsAt           string              `json:"ends_at"`
	Name             string              `json:"name"`
	DescriptionShort string              `json:"description_short"`
	URL              string              `json:"url"`
	PosterImage      imageDTO            `json:"poster_image"`
	Location         locationDTO         `json:"location"`
	Organization     organizationDTO     `json:"organization"`
	Categories       []categoryDTO       `json:"categories"`
	TicketTypes      []ticketTypeDTO     `json:"ticket_types"`
	AgeLimit         string              `json:"age_limit"`
	RegistrationData registrationDataDTO `json:"registration_data"`
}

type imageDTO struct {
	DefaultURL string `json:"default_url"`
}

type locationDTO struct {
	Country     string    `json:"country"`
	City        string    `json:"city"`
	Address     string    `json:"address"`
	Coordinates []float64 `json:"coordinates"`
}

type organizationDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type categoryDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type ticketTypeDTO struct {
	Price     float64 `json:"price"`
	IsActive  bool    `json:"is_active"`
	Remaining int     `json:"remaining"`
}

type registrationDataDTO struct {
	PriceMin           float64 `json:"price_min"`
	PriceMax           float64 `json:"price_max"`
	IsRegistrationOpen bool    `json:"is_registration_open"`
}

type flexibleInt int

func (value *flexibleInt) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	switch decoded := decoded.(type) {
	case json.Number:
		parsed, err := strconv.Atoi(decoded.String())
		if err != nil {
			return fmt.Errorf("decode timepad integer: %w", err)
		}
		*value = flexibleInt(parsed)
	case string:
		parsed, err := strconv.Atoi(decoded)
		if err != nil {
			return fmt.Errorf("decode timepad integer: %w", err)
		}
		*value = flexibleInt(parsed)
	default:
		return fmt.Errorf("decode timepad integer: unexpected JSON type")
	}
	return nil
}
