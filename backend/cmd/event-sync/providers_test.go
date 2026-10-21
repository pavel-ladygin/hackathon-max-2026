package main

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func TestBuiltInProvidersIndependentAndDisabledBeforeConstruction(t *testing.T) {
	profiles, _ := config.ParseSyncCityProfiles("")
	for _, combination := range []struct {
		k, tp bool
		want  int
	}{{false, false, 0}, {true, false, 1}, {false, true, 1}, {true, true, 2}} {
		cfg := config.Config{EventSyncKudaGoEnabled: combination.k, EventSyncTimepadEnabled: combination.tp,
			EventSyncCityID: uuid.MustParse(catalogseed.MoscowCityID), SyncCityProfiles: profiles,
			KudaGoBaseURL: "invalid-disabled-client", TimepadBaseURL: "invalid-disabled-client",
			KudaGoTimeout: time.Second, TimepadTimeout: time.Second, KudaGoLocation: "msk", KudaGoPageSize: 100,
			TimepadPageSize: 100, TimepadMaxRequestsPerMinute: 6, TimepadToken: "fixture-token"}
		if combination.k {
			cfg.KudaGoBaseURL = "https://kudago.com/public-api/v1.4"
		}
		if combination.tp {
			cfg.TimepadBaseURL = "https://api.timepad.ru/v1"
		}
		lock := func(string) func(context.Context) (func() error, bool, error) { return nil }
		for restart := 0; restart < 2; restart++ {
			got, err := builtInProviders(cfg, "Europe/Moscow", lock)
			if err != nil || len(got) != combination.want {
				t.Fatalf("k=%v tp=%v providers=%d error=%v", combination.k, combination.tp, len(got), err)
			}
		}
	}
}

func TestBlockingProviderStopsSubsequentCyclesIndependently(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		failed := &stubImporter{err: providers.HTTPError(status)}
		healthy := &stubImporter{}
		list := []syncProvider{{name: "kudago", importer: failed}, {name: "timepad", importer: healthy}}
		blocked := map[string]bool{}
		for tick := 0; tick < 3; tick++ {
			if err := syncProvidersCycle(context.Background(), uuid.New(), nil, list, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), blocked); err != nil {
				t.Fatal(err)
			}
		}
		if failed.calls != 1 || healthy.calls != 3 {
			t.Fatalf("status=%d blocked calls=%d healthy=%d", status, failed.calls, healthy.calls)
		}
	}
}
