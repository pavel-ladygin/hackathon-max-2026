package config

import (
	"testing"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
)

func TestSyncCityConfig(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("DATABASE_URL", "postgres://fixture@127.0.0.1:65426/multicity_fixture?sslmode=disable")
	t.Setenv("EVENT_SYNC_CITY_ID", "")
	t.Setenv("KUDAGO_LOCATION", "")
	t.Setenv("TIMEPAD_CITY_NAME", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EventSyncCityID.String() != catalogseed.MoscowCityID || cfg.KudaGoLocation != "msk" || cfg.KudaGoLocationExplicit || cfg.TimepadCityName != "" {
		t.Fatal("legacy Moscow configuration changed")
	}
	t.Setenv("EVENT_SYNC_CITY_ID", "3d3c83de-691b-510e-85e5-79685c4012ee")
	t.Setenv("KUDAGO_LOCATION", "kzn")
	t.Setenv("TIMEPAD_CITY_NAME", "Казань")
	cfg, err = Load()
	if err != nil || !cfg.KudaGoLocationExplicit || cfg.KudaGoLocation != "kzn" || cfg.TimepadCityName != "Казань" {
		t.Fatalf("explicit city configuration failed: %v", err)
	}
	for _, invalid := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		t.Setenv("EVENT_SYNC_CITY_ID", invalid)
		if _, err := Load(); err == nil {
			t.Fatal("invalid city accepted")
		}
	}
}
