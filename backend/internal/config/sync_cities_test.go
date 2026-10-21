package config

import (
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"testing"
)

func TestSaintPetersburgSyncProfile(t *testing.T) {
	profiles, err := ParseSyncCityProfiles("")
	if err != nil {
		t.Fatal(err)
	}
	profile := profiles[uuid.MustParse("bf61fbf5-8c5b-53da-96c7-3560df3f8177")]
	if profile.Name != "Санкт-Петербург" || profile.Timezone != "Europe/Moscow" || profile.KudaGoLocation != "spb" || profile.TimepadCityName != "Санкт-Петербург" {
		t.Fatalf("profile=%+v", profile)
	}
}

func TestSyncCityProfileValidation(t *testing.T) {
	third := uuid.New()
	raw := `[{"city_id":"` + third.String() + `","name":"Екатеринбург","timezone":"Asia/Yekaterinburg","kudago_location":"ekb","timepad_city_name":"Екатеринбург"}]`
	profiles, err := ParseSyncCityProfiles(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		cfg := Config{EventSyncCityID: p.CityID, KudaGoLocation: p.KudaGoLocation, KudaGoLocationExplicit: true, TimepadCityName: p.TimepadCityName, SyncCityProfiles: profiles}
		if err := cfg.ValidateSyncCity(p.Name, p.Timezone); err != nil {
			t.Fatal(err)
		}
		for _, change := range []func(*Config){func(c *Config) { c.KudaGoLocation = "wrong" }, func(c *Config) { c.TimepadCityName = "Wrong" }, func(c *Config) { c.EventSyncCityID = uuid.New() }} {
			bad := cfg
			change(&bad)
			if bad.ValidateSyncCity(p.Name, p.Timezone) == nil {
				t.Fatal("mismatch accepted")
			}
		}
		if cfg.ValidateSyncCity("another city", p.Timezone) == nil || cfg.ValidateSyncCity(p.Name, "UTC") == nil {
			t.Fatal("DB metadata mismatch accepted")
		}
		cfg.TimepadCityName = ""
		if err := cfg.ValidateSyncCity(p.Name, p.Timezone); err != nil {
			t.Fatal("disabled Timepad should be allowed", err)
		}
	}
	for _, raw := range []string{`{}`, `[] []`, `[{"city_id":"bad"}]`, `[{"city_id":"` + catalogseed.MoscowCityID + `","name":"Other","timezone":"UTC","kudago_location":"other"}]`,
		`[{"city_id":"` + third.String() + `","name":"Other","timezone":"UTC","kudago_location":"msk"}]`,
		`[{"city_id":"` + third.String() + `","name":"Other","timezone":"UTC","kudago_location":"other","timepad_city_name":"Москва"}]`,
		`[{"city_id":"` + third.String() + `","name":"Other","timezone":"Invalid/Zone","kudago_location":"other"}]`,
		`[{"city_id":"` + third.String() + `","name":"Other","timezone":"UTC","kudago_location":"../other"}]`} {
		if _, err := ParseSyncCityProfiles(raw); err == nil {
			t.Errorf("invalid profiles accepted: %s", raw)
		}
	}
}
