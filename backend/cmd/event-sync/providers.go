package main

import (
	"context"
	"errors"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/kudago"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/timepad"
)

// Disabled providers are omitted before constructing any network client.
func builtInProviders(cfg config.Config, timezone string, lock func(string) func(context.Context) (func() error, bool, error)) ([]syncProvider, error) {
	var result []syncProvider
	if cfg.EventSyncKudaGoEnabled {
		client, err := kudago.NewClient(kudago.Options{
			BaseURL: cfg.KudaGoBaseURL, Timeout: cfg.KudaGoTimeout, Location: cfg.KudaGoLocation, PageSize: cfg.KudaGoPageSize,
			CityID: cfg.EventSyncCityID, Timezone: timezone, MaxRequestsPerMinute: cfg.KudaGoMaxRequestsPerMinute,
			MaxPagesPerRun: cfg.KudaGoMaxPagesPerRun, MaxRunDuration: cfg.EventSyncMaxRunDuration, UpsertOnly: cfg.EventSyncUpsertOnly,
		})
		if err != nil {
			return nil, err
		}
		result = append(result, syncProvider{name: "kudago", priority: kudaGoPriority, importer: client, acquireLock: lock("kudago")})
	}
	if cfg.EventSyncTimepadEnabled {
		profile, ok := cfg.SyncCityProfiles[cfg.EventSyncCityID]
		city := cfg.TimepadCityName
		if city == "" && ok {
			city = profile.TimepadCityName
		}
		if city == "" {
			return nil, errors.New("enabled Timepad requires a city profile")
		}
		client, err := timepad.NewClient(timepad.Options{
			BaseURL: cfg.TimepadBaseURL, Token: cfg.TimepadToken, Timeout: cfg.TimepadTimeout, PageSize: cfg.TimepadPageSize,
			MaxRequestsPerMinute: cfg.TimepadMaxRequestsPerMinute, CityID: cfg.EventSyncCityID, CityName: city, Timezone: timezone,
			MaxPagesPerRun: cfg.TimepadMaxPagesPerRun, MaxRunDuration: cfg.EventSyncMaxRunDuration, UpsertOnly: cfg.EventSyncUpsertOnly,
		})
		if err != nil {
			return nil, err
		}
		result = append(result, syncProvider{name: "timepad", priority: timepadPriority, importer: client, acquireLock: lock("timepad")})
	}
	return result, nil
}
