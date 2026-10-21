package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ProviderEnabled fails closed and does not include the supplied value in errors.
func ProviderEnabled(name string) (bool, error) { return strictBool(name, false) }

func strictBool(name string, fallback bool) (bool, error) {
	value, present := os.LookupEnv(name)
	if !present {
		return fallback, nil
	}
	switch strings.TrimSpace(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func (cfg *Config) loadProviderSafety() error {
	for name, target := range map[string]*bool{
		"EVENT_SYNC_KUDAGO_ENABLED":  &cfg.EventSyncKudaGoEnabled,
		"EVENT_SYNC_TIMEPAD_ENABLED": &cfg.EventSyncTimepadEnabled,
		"EVENT_SYNC_UPSERT_ONLY":     &cfg.EventSyncUpsertOnly,
	} {
		value, err := strictBool(name, name == "EVENT_SYNC_UPSERT_ONLY")
		if err != nil {
			return err
		}
		*target = value
	}
	for name, target := range map[string]*int{
		"KUDAGO_MAX_PAGES_PER_RUN":       &cfg.KudaGoMaxPagesPerRun,
		"TIMEPAD_MAX_PAGES_PER_RUN":      &cfg.TimepadMaxPagesPerRun,
		"KUDAGO_MAX_REQUESTS_PER_MINUTE": &cfg.KudaGoMaxRequestsPerMinute,
	} {
		if value, present := os.LookupEnv(name); present {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			min, max := 0, 1000
			if strings.Contains(name, "REQUESTS") {
				min, max = 1, 30
			}
			if err != nil || n < min || n > max {
				return fmt.Errorf("%s is invalid", name)
			}
			*target = n
		}
	}
	if value, present := os.LookupEnv("EVENT_SYNC_MAX_RUN_DURATION"); present {
		duration, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil || duration < time.Second || duration > 2*time.Hour {
			return fmt.Errorf("EVENT_SYNC_MAX_RUN_DURATION is invalid")
		}
		cfg.EventSyncMaxRunDuration = duration
	}
	if cfg.EventSyncTimepadEnabled && cfg.TimepadToken == "" {
		return fmt.Errorf("TIMEPAD_TOKEN is required when Timepad is enabled")
	}
	return nil
}
