// Package generic converts records from ordinary JSON event APIs into the
// provider-neutral event model.
package generic

import (
	"fmt"
	"strings"
)

// Config describes how one source record maps to a normalized event.
type Config struct {
	SourceKey string       `json:"source_key"`
	Fields    Fields       `json:"mapping"`
	Defaults  Defaults     `json:"defaults"`
	PriceUnit PriceUnit    `json:"price_unit"`
	PriceFrom FieldMapping `json:"price_from"`
	PriceTo   FieldMapping `json:"price_to"`
}

// Fields contains dot-path mappings for supported normalized event fields.
type Fields struct {
	ExternalID      FieldMapping `json:"external_id"`
	Title           FieldMapping `json:"title"`
	Description     FieldMapping `json:"description"`
	Subtitle        FieldMapping `json:"subtitle"`
	StartsAt        FieldMapping `json:"starts_at"`
	EndsAt          FieldMapping `json:"ends_at"`
	VenueName       FieldMapping `json:"venue_name"`
	VenueAddress    FieldMapping `json:"venue_address"`
	Latitude        FieldMapping `json:"latitude"`
	Longitude       FieldMapping `json:"longitude"`
	Metro           FieldMapping `json:"metro"`
	Image           FieldMapping `json:"image"`
	TicketURL       FieldMapping `json:"ticket_url"`
	TicketAvailable FieldMapping `json:"ticket_available"`
}

// FieldMapping selects a value from a JSON record and optionally converts it.
// Default is used only when Path cannot be resolved.
type FieldMapping struct {
	Path      string    `json:"path"`
	Transform Transform `json:"transform,omitempty"`
	Default   *string   `json:"default,omitempty"`
}

// Defaults are source-wide values required or commonly shared by events.
type Defaults struct {
	Category string `json:"category"`
	Timezone string `json:"timezone"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

// PriceUnit describes the units in which mapped source prices are expressed.
type PriceUnit string

const (
	PriceUnitMajor PriceUnit = "major"
	PriceUnitMinor PriceUnit = "minor"
)

// Transform is a fixed, allow-listed conversion applied to a mapped value.
type Transform string

const (
	TransformNone          Transform = ""
	TransformString        Transform = "string"
	TransformNumber        Transform = "number"
	TransformISODateTime   Transform = "iso_datetime"
	TransformUnixTimestamp Transform = "unix_timestamp"
	TransformStripHTML     Transform = "strip_html"
)

// ValidateMappings verifies configured paths and conversions before a source
// is saved or fetched. Runtime values are still validated while normalizing.
func ValidateMappings(fields Fields, priceFrom, priceTo FieldMapping) error {
	stringFields := []struct {
		name    string
		mapping FieldMapping
	}{
		{"external_id", fields.ExternalID}, {"title", fields.Title},
		{"description", fields.Description}, {"subtitle", fields.Subtitle},
		{"venue_name", fields.VenueName}, {"venue_address", fields.VenueAddress},
		{"metro", fields.Metro}, {"image", fields.Image}, {"ticket_url", fields.TicketURL},
	}
	for _, field := range stringFields {
		if err := validateFieldMapping(field.name, field.mapping, TransformString, TransformStripHTML); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name    string
		mapping FieldMapping
	}{
		{"starts_at", fields.StartsAt}, {"ends_at", fields.EndsAt},
	} {
		if err := validateFieldMapping(field.name, field.mapping, TransformISODateTime, TransformUnixTimestamp); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name    string
		mapping FieldMapping
	}{
		{"latitude", fields.Latitude}, {"longitude", fields.Longitude},
		{"price_from", priceFrom}, {"price_to", priceTo},
	} {
		if err := validateFieldMapping(field.name, field.mapping, TransformNumber); err != nil {
			return err
		}
	}
	return validateFieldMapping("ticket_available", fields.TicketAvailable)
}

func validateFieldMapping(name string, mapping FieldMapping, supported ...Transform) error {
	if path := strings.TrimSpace(mapping.Path); path != "" {
		for _, segment := range strings.Split(path, ".") {
			if strings.TrimSpace(segment) == "" {
				return fmt.Errorf("%s mapping path is invalid", name)
			}
		}
	}
	if mapping.Transform == TransformNone {
		return nil
	}
	for _, transform := range supported {
		if mapping.Transform == transform {
			return nil
		}
	}
	return fmt.Errorf("%s mapping transform is unsupported", name)
}
