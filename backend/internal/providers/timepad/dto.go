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
	Categories       categoriesDTO       `json:"categories"`
	TicketTypes      ticketTypesDTO      `json:"ticket_types"`
	AgeLimit         flexibleText        `json:"age_limit"`
	AccessStatus     string              `json:"access_status"`
	ModerationStatus string              `json:"moderation_status"`
	RegistrationData registrationDataDTO `json:"registration_data"`
}

type imageDTO struct {
	DefaultURL string `json:"default_url"`
}

type locationDTO struct {
	Country     string         `json:"country"`
	City        string         `json:"city"`
	Address     string         `json:"address"`
	Coordinates coordinatesDTO `json:"coordinates"`
}

type coordinatesDTO []float64

func (coordinates *coordinatesDTO) UnmarshalJSON(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		*coordinates = nil
		return nil
	}
	decoded := make([]float64, 0, len(values))
	for _, value := range values {
		var number json.Number
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		if err := decoder.Decode(&number); err != nil {
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				*coordinates = nil
				return nil
			}
			number = json.Number(text)
		}
		parsed, err := strconv.ParseFloat(number.String(), 64)
		if err != nil {
			*coordinates = nil
			return nil
		}
		decoded = append(decoded, parsed)
	}
	*coordinates = decoded
	return nil
}

type organizationDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type categoryDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type categoriesDTO struct {
	Values    []categoryDTO
	Malformed bool
}

func (categories *categoriesDTO) UnmarshalJSON(data []byte) error {
	var values []categoryDTO
	if err := json.Unmarshal(data, &values); err == nil {
		categories.Values = values
		categories.Malformed = false
		return nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err == nil && len(object) == 0 {
		categories.Values = nil
		categories.Malformed = false
		return nil
	}

	categories.Values = nil
	categories.Malformed = true
	return nil
}

type ticketTypesDTO []ticketTypeDTO

type ticketTypeDTO struct {
	Price    float64
	IsActive bool
}

// UnmarshalJSON tolerates optional ticket data without allowing an unsupported
// value to turn into a free ticket. Non-array forms are treated as absent.
func (tickets *ticketTypesDTO) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*tickets = nil
		return nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		if !json.Valid(data) {
			return err
		}
		*tickets = nil
		return nil
	}

	decoded := make(ticketTypesDTO, 0, len(entries))
	for _, entry := range entries {
		var wire struct {
			Price    json.RawMessage `json:"price"`
			IsActive *bool           `json:"is_active"`
		}
		if err := json.Unmarshal(entry, &wire); err != nil || wire.IsActive == nil {
			*tickets = nil
			return nil
		}
		if !*wire.IsActive {
			continue
		}
		price := decodeOptional[*float64](wire.Price)
		if price == nil {
			// A partial set cannot establish a reliable minimum or maximum.
			// A complete registration range can still supply the event price.
			*tickets = nil
			return nil
		}
		decoded = append(decoded, ticketTypeDTO{Price: *price, IsActive: true})
	}
	if len(decoded) == 0 {
		*tickets = nil
		return nil
	}
	*tickets = decoded
	return nil
}

type registrationDataDTO struct {
	PriceMin           *float64
	PriceMax           *float64
	IsRegistrationOpen bool
}

// UnmarshalJSON decodes each optional registration field separately so a
// malformed price bound cannot discard a usable companion bound or make a
// provider page undecodable.
func (registration *registrationDataDTO) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		if !json.Valid(data) {
			return err
		}
		*registration = registrationDataDTO{}
		return nil
	}

	*registration = registrationDataDTO{
		PriceMin: decodeOptional[*float64](fields["price_min"]),
		PriceMax: decodeOptional[*float64](fields["price_max"]),
	}
	if raw, ok := fields["is_registration_open"]; ok {
		registration.IsRegistrationOpen = decodeOptional[bool](raw)
	}
	return nil
}

// flexibleText isolates Timepad's inconsistent optional scalar encoding. In
// live responses age_limit is observed both as a JSON string ("18+") and as a
// number (18). Unsupported optional shapes are treated as absent so one bad
// provider record cannot make the entire page undecodable.
type flexibleText string

func (value *flexibleText) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*value = flexibleText(text)
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	switch decoded := decoded.(type) {
	case json.Number:
		*value = flexibleText(decoded.String())
	default:
		*value = ""
	}
	return nil
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
