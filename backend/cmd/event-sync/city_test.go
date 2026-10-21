package main

import (
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
)

func TestSyncCityRequiresExplicitProviderLocation(t *testing.T) {
	cfg := config.Config{EventSyncCityID: uuid.MustParse(catalogseed.MoscowCityID), KudaGoLocation: "msk"}
	if err := validateSyncCity(cfg, "Москва", "Europe/Moscow"); err != nil {
		t.Fatal(err)
	}
	cfg.EventSyncCityID = uuid.New()
	if err := validateSyncCity(cfg, "Екатеринбург", "Asia/Yekaterinburg"); err == nil {
		t.Fatal("implicit Moscow provider location accepted for another city")
	}
	cfg.KudaGoLocationExplicit = true
	cfg.KudaGoLocation = "ekb"
	if err := validateSyncCity(cfg, "Екатеринбург", "Asia/Yekaterinburg"); err == nil {
		t.Fatal("unknown city accepted without profile")
	}
	cfg.SyncCityProfiles, _ = config.ParseSyncCityProfiles(`[{"city_id":"` + cfg.EventSyncCityID.String() + `","name":"Екатеринбург","timezone":"Asia/Yekaterinburg","kudago_location":"ekb","timepad_city_name":"Екатеринбург"}]`)
	if err := validateSyncCity(cfg, "Екатеринбург", "Asia/Yekaterinburg"); err != nil {
		t.Fatal(err)
	}
	if err := validateSyncCity(cfg, "Екатеринбург", "invalid/zone"); err == nil {
		t.Fatal("invalid city timezone accepted")
	}
	cfg.KudaGoLocation = "msk"
	if err := validateSyncCity(cfg, "Екатеринбург", "Asia/Yekaterinburg"); err == nil {
		t.Fatal("deployment-injected Moscow default accepted for another city")
	}
}

func TestGenericSyncWithoutRunnerCannotSelectAnotherCity(t *testing.T) {
	sources := []sourceconfig.Source{{SourceKey: "generic:fixture", Enabled: true}}
	if got := genericSyncProviders(sources, nil, uuid.New()); len(got) != 0 {
		t.Fatal("source without confirmed city binding scheduled")
	}
}

func TestGenericSchedulerIgnoresSystemSource(t *testing.T) {
	if got := genericSyncProviders([]sourceconfig.Source{{SourceKey: "user", Enabled: true}}, nil); len(got) != 0 {
		t.Fatal("system source was scheduled as Generic")
	}
}

func TestSyncCityRejectsMismatchedProviderNames(t *testing.T) {
	for _, tc := range []struct{ city, location, name string }{
		{"3d3c83de-691b-510e-85e5-79685c4012ee", "spb", "Казань"},
		{"3d3c83de-691b-510e-85e5-79685c4012ee", "kzn", "Москва"},
		{catalogseed.MoscowCityID, "kzn", "Москва"},
	} {
		t.Run(tc.city+tc.location+tc.name, func(t *testing.T) {
			cfg := config.Config{EventSyncCityID: uuid.MustParse(tc.city), KudaGoLocation: tc.location, KudaGoLocationExplicit: true, TimepadCityName: tc.name}
			name := "Казань"
			if tc.city == catalogseed.MoscowCityID {
				name = "Москва"
			}
			if err := validateSyncCity(cfg, name, "Europe/Moscow"); err == nil {
				t.Fatal("mismatched provider city accepted")
			}
		})
	}
}
