package config

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
)

// SyncCityProfile binds a catalog identity to provider identifiers. Additional
// cities require an explicit deployment declaration and matching DB metadata;
// a free-form provider location is never sufficient to infer city ownership.
type SyncCityProfile struct {
	CityID          uuid.UUID `json:"city_id"`
	Name            string    `json:"name"`
	Timezone        string    `json:"timezone"`
	KudaGoLocation  string    `json:"kudago_location"`
	TimepadCityName string    `json:"timepad_city_name"`
}

func ParseSyncCityProfiles(raw string) (map[uuid.UUID]SyncCityProfile, error) {
	profiles := []SyncCityProfile{
		{uuid.MustParse("bf61fbf5-8c5b-53da-96c7-3560df3f8177"), "Санкт-Петербург", "Europe/Moscow", "spb", "Санкт-Петербург"},
		{uuid.MustParse(catalogseed.MoscowCityID), "Москва", "Europe/Moscow", "msk", "Москва"},
		{uuid.MustParse("3d3c83de-691b-510e-85e5-79685c4012ee"), "Казань", "Europe/Moscow", "kzn", "Казань"},
	}
	if strings.TrimSpace(raw) != "" {
		var additional []SyncCityProfile
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&additional); err != nil {
			return nil, errors.New("EVENT_SYNC_CITY_PROFILES is invalid")
		}
		if err := d.Decode(new(any)); err != io.EOF {
			return nil, errors.New("EVENT_SYNC_CITY_PROFILES has trailing data")
		}
		profiles = append(profiles, additional...)
	}
	result := make(map[uuid.UUID]SyncCityProfile, len(profiles))
	names, locations, timepadNames := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, p := range profiles {
		p.Name = strings.TrimSpace(p.Name)
		p.Timezone = strings.TrimSpace(p.Timezone)
		p.KudaGoLocation = strings.ToLower(strings.TrimSpace(p.KudaGoLocation))
		p.TimepadCityName = strings.TrimSpace(p.TimepadCityName)
		if p.CityID == uuid.Nil || p.Name == "" || p.KudaGoLocation == "" || p.Timezone == "" {
			return nil, errors.New("sync city profile is incomplete")
		}
		for _, ch := range p.KudaGoLocation {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return nil, errors.New("sync city provider location is invalid")
			}
		}
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return nil, errors.New("sync city profile timezone is invalid")
		}
		name, tp := strings.ToLower(p.Name), strings.ToLower(p.TimepadCityName)
		if _, exists := result[p.CityID]; exists || names[name] || locations[p.KudaGoLocation] || (tp != "" && timepadNames[tp]) {
			return nil, errors.New("sync city profiles contain conflicting identities")
		}
		result[p.CityID] = p
		names[name] = true
		locations[p.KudaGoLocation] = true
		if tp != "" {
			timepadNames[tp] = true
		}
	}
	return result, nil
}

// ValidateSyncCity checks both the internal metadata and the chosen provider
// filters before the worker creates any sync run or writes catalog data.
func (cfg Config) ValidateSyncCity(name, timezone string) error {
	profiles := cfg.SyncCityProfiles
	if profiles == nil {
		var err error
		profiles, err = ParseSyncCityProfiles("")
		if err != nil {
			return err
		}
	}
	profile, ok := profiles[cfg.EventSyncCityID]
	if !ok {
		return errors.New("sync city requires an explicit validated provider profile")
	}
	if !strings.EqualFold(strings.TrimSpace(name), profile.Name) || timezone != profile.Timezone {
		return errors.New("catalog city metadata differs from the configured provider profile")
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.KudaGoLocation), profile.KudaGoLocation) {
		return errors.New("KUDAGO_LOCATION does not match the configured catalog city")
	}
	if cfg.EventSyncCityID.String() != catalogseed.MoscowCityID && !cfg.KudaGoLocationExplicit {
		return errors.New("non-Moscow sync requires explicit KUDAGO_LOCATION")
	}
	if cfg.TimepadCityName != "" && !strings.EqualFold(strings.TrimSpace(cfg.TimepadCityName), profile.TimepadCityName) {
		return errors.New("TIMEPAD_CITY_NAME does not match the configured catalog city")
	}
	return nil
}
