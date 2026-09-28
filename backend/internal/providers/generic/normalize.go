package generic

import (
	"errors"
	"fmt"
	"html"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

// Normalize maps one JSON object into the canonical provider event model.
func Normalize(config Config, record map[string]any) (providers.NormalizedEvent, error) {
	if record == nil {
		return providers.NormalizedEvent{}, errors.New("record is required")
	}
	source := strings.TrimSpace(config.SourceKey)
	if !strings.HasPrefix(source, "generic:") || strings.TrimSpace(strings.TrimPrefix(source, "generic:")) == "" {
		return providers.NormalizedEvent{}, errors.New("source_key must use the generic:<slug> format")
	}
	if err := ValidateMappings(config.Fields, config.PriceFrom, config.PriceTo); err != nil {
		return providers.NormalizedEvent{}, err
	}
	tz := strings.TrimSpace(config.Defaults.Timezone)
	if tz == "" {
		return providers.NormalizedEvent{}, errors.New("default timezone is required")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return providers.NormalizedEvent{}, fmt.Errorf("default timezone is invalid")
	}
	category := strings.TrimSpace(config.Defaults.Category)
	if category == "" {
		return providers.NormalizedEvent{}, errors.New("default category is required")
	}
	currency := strings.TrimSpace(config.Defaults.Currency)
	if currency == "" {
		return providers.NormalizedEvent{}, errors.New("default currency is required")
	}
	status := strings.TrimSpace(config.Defaults.Status)
	if status == "" {
		return providers.NormalizedEvent{}, errors.New("default status is required")
	}
	if status != providers.EventStatusPublished && status != providers.EventStatusSoldOut && status != providers.EventStatusCancelled {
		return providers.NormalizedEvent{}, errors.New("default status is invalid")
	}
	if config.PriceUnit != "" && config.PriceUnit != PriceUnitMajor && config.PriceUnit != PriceUnitMinor {
		return providers.NormalizedEvent{}, errors.New("price unit must be major or minor")
	}
	if config.PriceUnit == "" {
		config.PriceUnit = PriceUnitMajor
	}

	var event providers.NormalizedEvent
	event.Source = source
	event.Timezone = tz
	event.Currency = currency
	event.Status = status
	event.ProviderActive = true
	event.Categories = []providers.NormalizedCategory{{Slug: category, Weight: 1, IsPrimary: true}}

	if event.ExternalID, err = requiredString(record, "external_id", config.Fields.ExternalID); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if event.Title, err = requiredString(record, "title", config.Fields.Title); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if event.Venue.Name, err = requiredString(record, "venue.name", config.Fields.VenueName); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if event.StartsAt, err = requiredTime(record, "starts_at", config.Fields.StartsAt, loc); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if event.Description, err = optionalString(record, "description", config.Fields.Description); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if v, e := optionalString(record, "subtitle", config.Fields.Subtitle); e != nil {
		return providers.NormalizedEvent{}, e
	} else if v != "" {
		event.Subtitle = &v
	}
	if v, e := optionalTime(record, "ends_at", config.Fields.EndsAt, loc); e != nil {
		return providers.NormalizedEvent{}, e
	} else if v != nil {
		event.EndsAt = v
	}
	if event.Venue.Address, err = optionalString(record, "venue.address", config.Fields.VenueAddress); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if event.Venue.Metro, err = optionalStringPointer(record, "venue.metro", config.Fields.Metro); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if event.Venue.Latitude, err = optionalFloat(record, "venue.latitude", config.Fields.Latitude); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if event.Venue.Longitude, err = optionalFloat(record, "venue.longitude", config.Fields.Longitude); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if v, e := optionalString(record, "ticket_url", config.Fields.TicketURL); e != nil {
		return providers.NormalizedEvent{}, e
	} else if v != "" {
		event.TicketURL = &v
	}
	if event.TicketAvailable, err = optionalBool(record, "ticket_available", config.Fields.TicketAvailable); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if event.PriceFromMinor, err = optionalPrice(record, "price_from", config.PriceFrom, config.PriceUnit); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if event.PriceToMinor, err = optionalPrice(record, "price_to", config.PriceTo, config.PriceUnit); err != nil {
		return providers.NormalizedEvent{}, err
	}
	if image, e := optionalString(record, "image", config.Fields.Image); e != nil {
		return providers.NormalizedEvent{}, e
	} else if image != "" {
		event.Images = []providers.NormalizedImage{{URL: image, Role: "card", Position: 0}}
	}
	return event, nil
}

func requiredString(record map[string]any, name string, mapping FieldMapping) (string, error) {
	v, err := mappedValue(record, name, mapping)
	if err != nil {
		return "", err
	}
	s, err := applyStringTransform(v, mapping.Transform)
	if err != nil || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%s is required or invalid", name)
	}
	return strings.TrimSpace(s), nil
}

func optionalString(record map[string]any, name string, mapping FieldMapping) (string, error) {
	v, found, err := resolveMapping(record, name, mapping)
	if err != nil || !found {
		return "", err
	}
	s, err := applyStringTransform(v, mapping.Transform)
	if err != nil {
		return "", fmt.Errorf("%s is invalid", name)
	}
	return strings.TrimSpace(s), nil
}

func optionalStringPointer(record map[string]any, name string, mapping FieldMapping) (*string, error) {
	s, err := optionalString(record, name, mapping)
	if err != nil || s == "" {
		return nil, err
	}
	return &s, nil
}

func requiredTime(record map[string]any, name string, mapping FieldMapping, loc *time.Location) (time.Time, error) {
	v, err := mappedValue(record, name, mapping)
	if err != nil {
		return time.Time{}, err
	}
	t, err := convertTime(v, mapping.Transform, loc)
	if err != nil || t.IsZero() {
		return time.Time{}, fmt.Errorf("%s is required or invalid", name)
	}
	return t, nil
}

func optionalTime(record map[string]any, name string, mapping FieldMapping, loc *time.Location) (*time.Time, error) {
	v, found, err := resolveMapping(record, name, mapping)
	if err != nil || !found {
		return nil, err
	}
	t, err := convertTime(v, mapping.Transform, loc)
	if err != nil {
		return nil, fmt.Errorf("%s is invalid", name)
	}
	return &t, nil
}

func optionalFloat(record map[string]any, name string, mapping FieldMapping) (*float64, error) {
	v, found, err := resolveMapping(record, name, mapping)
	if err != nil || !found {
		return nil, err
	}
	n, err := convertNumber(v, mapping.Transform)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, fmt.Errorf("%s is invalid", name)
	}
	if (name == "venue.latitude" && (n < -90 || n > 90)) || (name == "venue.longitude" && (n < -180 || n > 180)) {
		return nil, fmt.Errorf("%s is out of range", name)
	}
	return &n, nil
}

func optionalPrice(record map[string]any, name string, mapping FieldMapping, unit PriceUnit) (*int32, error) {
	v, found, err := resolveMapping(record, name, mapping)
	if err != nil || !found {
		return nil, err
	}
	n, err := convertNumber(v, mapping.Transform)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, fmt.Errorf("%s is invalid", name)
	}
	if n < 0 {
		return nil, fmt.Errorf("%s is out of range", name)
	}
	if unit == PriceUnitMajor {
		n *= 100
	}
	n = math.Round(n)
	if n < math.MinInt32 || n > math.MaxInt32 {
		return nil, fmt.Errorf("%s is out of range", name)
	}
	minor := int32(n)
	return &minor, nil
}

func optionalBool(record map[string]any, name string, mapping FieldMapping) (bool, error) {
	v, found, err := resolveMapping(record, name, mapping)
	if err != nil || !found {
		return false, err
	}
	switch b := v.(type) {
	case bool:
		return b, nil
	case string:
		parsed, parseErr := strconv.ParseBool(strings.TrimSpace(b))
		if parseErr == nil {
			return parsed, nil
		}
	}
	return false, fmt.Errorf("%s is invalid", name)
}

func mappedValue(record map[string]any, name string, mapping FieldMapping) (any, error) {
	v, found, err := resolveMapping(record, name, mapping)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s is required", name)
	}
	return v, nil
}

func resolveMapping(record map[string]any, name string, mapping FieldMapping) (any, bool, error) {
	if strings.TrimSpace(mapping.Path) != "" {
		v, found, err := resolvePath(record, mapping.Path)
		if err != nil {
			return nil, false, fmt.Errorf("%s mapping path is invalid", name)
		}
		if found && v != nil {
			return v, true, nil
		}
	}
	if mapping.Default != nil {
		return *mapping.Default, true, nil
	}
	return nil, false, nil
}

func resolvePath(root any, path string) (any, bool, error) {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if path == "" {
		return nil, false, errors.New("empty path")
	}
	var current any = root
	for _, part := range parts {
		if part == "" {
			return nil, false, errors.New("empty path segment")
		}
		switch value := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = value[part]
			if !ok {
				return nil, false, nil
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(value) {
				return nil, false, nil
			}
			current = value[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func applyStringTransform(value any, transform Transform) (string, error) {
	switch transform {
	case TransformNone, TransformString:
		return toString(value)
	case TransformStripHTML:
		s, err := toString(value)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(html.UnescapeString(htmlTagPattern.ReplaceAllString(s, ""))), nil
	default:
		return "", errors.New("unsupported transform")
	}
}

func toString(value any) (string, error) {
	if value == nil {
		return "", errors.New("null value")
	}
	switch v := value.(type) {
	case string:
		return v, nil
	case fmt.Stringer:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'f', -1, rv.Type().Bits()), nil
	}
	return "", errors.New("value is not scalar")
}

func convertNumber(value any, transform Transform) (float64, error) {
	if transform != TransformNone && transform != TransformNumber {
		return 0, errors.New("unsupported numeric transform")
	}
	s, err := toString(value)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func convertTime(value any, transform Transform, loc *time.Location) (time.Time, error) {
	switch transform {
	case TransformUnixTimestamp:
		n, err := convertNumber(value, TransformNumber)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < math.MinInt64 || n > math.MaxInt64 {
			return time.Time{}, errors.New("invalid timestamp")
		}
		seconds := int64(n)
		nanos := int64(math.Round((n - float64(seconds)) * 1e9))
		return time.Unix(seconds, nanos).In(loc), nil
	case TransformNone, TransformISODateTime:
		s, err := toString(value)
		if err != nil {
			return time.Time{}, err
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
			if strings.Contains(layout, "Z07") {
				if t, parseErr := time.Parse(layout, strings.TrimSpace(s)); parseErr == nil {
					return t, nil
				}
				continue
			}
			if t, parseErr := time.ParseInLocation(layout, strings.TrimSpace(s), loc); parseErr == nil {
				return t, nil
			}
		}
		return time.Time{}, errors.New("invalid datetime")
	default:
		return time.Time{}, errors.New("unsupported datetime transform")
	}
}
