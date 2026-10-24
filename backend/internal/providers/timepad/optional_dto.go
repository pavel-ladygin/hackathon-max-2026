package timepad

import "encoding/json"

// Keep identity, start time, city and eligibility fields strict. Optional
// presentation metadata may be dropped without rejecting the event or page.
func (event *eventDTO) UnmarshalJSON(data []byte) error {
	type strictEvent eventDTO
	var decoded strictEvent
	wire := struct {
		*strictEvent
		CreatedAt        json.RawMessage `json:"created_at"`
		EndsAt           json.RawMessage `json:"ends_at"`
		DescriptionShort json.RawMessage `json:"description_short"`
		URL              json.RawMessage `json:"url"`
		PosterImage      json.RawMessage `json:"poster_image"`
		Organization     json.RawMessage `json:"organization"`
	}{strictEvent: &decoded}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	decoded.CreatedAt = decodeOptional[string](wire.CreatedAt)
	decoded.EndsAt = decodeOptional[string](wire.EndsAt)
	decoded.DescriptionShort = decodeOptional[string](wire.DescriptionShort)
	decoded.URL = decodeOptional[string](wire.URL)
	decoded.PosterImage = decodeOptional[imageDTO](wire.PosterImage)
	organization := decodeOptional[struct {
		ID   json.RawMessage `json:"id"`
		Name json.RawMessage `json:"name"`
	}](wire.Organization)
	decoded.Organization = organizationDTO{
		ID:   decodeOptional[int64](organization.ID),
		Name: decodeOptional[string](organization.Name),
	}
	*event = eventDTO(decoded)
	return nil
}

func (location *locationDTO) UnmarshalJSON(data []byte) error {
	type strictLocation locationDTO
	var decoded strictLocation
	wire := struct {
		*strictLocation
		Country json.RawMessage `json:"country"`
		Address json.RawMessage `json:"address"`
	}{strictLocation: &decoded}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	decoded.Country = decodeOptional[string](wire.Country)
	decoded.Address = decodeOptional[string](wire.Address)
	*location = locationDTO(decoded)
	return nil
}

// Decode into a temporary value so a type error never exposes partial data.
func decodeOptional[T any](data json.RawMessage) T {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		var absent T
		return absent
	}
	return value
}
