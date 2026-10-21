package sourceconfig

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

// CityBindings is deployment configuration, not a new HTTP or schema field.
// One Generic source identity belongs to one city. Use distinct source keys for
// providers whose IDs are only locally unique. All processes must use the same
// bindings; repository guards reject attempts to move existing identities.
type CityBindings map[string]uuid.UUID

func LoadCityBindings() (CityBindings, error) {
	return ParseCityBindings(os.Getenv("EVENT_SOURCE_CITY_BINDINGS"))
}

func ParseCityBindings(raw string) (CityBindings, error) {
	bindings := CityBindings{}
	if strings.TrimSpace(raw) == "" {
		return bindings, nil
	}
	// Decode tokens to reject duplicate keys, which ordinary map decoding hides.
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("EVENT_SOURCE_CITY_BINDINGS is invalid")
	}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || !strings.HasPrefix(key, "generic:") || strings.TrimSpace(strings.TrimPrefix(key, "generic:")) == "" || strings.TrimSpace(key) != key {
			return nil, errors.New("EVENT_SOURCE_CITY_BINDINGS contains an invalid source")
		}
		if _, duplicate := bindings[key]; duplicate {
			return nil, errors.New("EVENT_SOURCE_CITY_BINDINGS contains a duplicate source")
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("EVENT_SOURCE_CITY_BINDINGS contains an invalid city")
		}
		city, err := uuid.Parse(value)
		if err != nil || city == uuid.Nil {
			return nil, errors.New("EVENT_SOURCE_CITY_BINDINGS contains an invalid city")
		}
		bindings[key] = city
	}
	if _, err := decoder.Token(); err != nil {
		return nil, errors.New("EVENT_SOURCE_CITY_BINDINGS is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, errors.New("EVENT_SOURCE_CITY_BINDINGS has trailing data")
	} else if !errors.Is(err, io.EOF) {
		return nil, errors.New("EVENT_SOURCE_CITY_BINDINGS is invalid")
	}
	return bindings, nil
}
