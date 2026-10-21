package config

import (
	"strings"
	"testing"
	"time"
)

func TestProviderSafetyConfiguration(t *testing.T) {
	setValidConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EventSyncKudaGoEnabled || cfg.EventSyncTimepadEnabled || !cfg.EventSyncUpsertOnly || cfg.KudaGoMaxPagesPerRun != 20 || cfg.TimepadMaxPagesPerRun != 20 || cfg.EventSyncMaxRunDuration != 45*time.Minute || cfg.KudaGoMaxRequestsPerMinute != 6 {
		t.Fatal("unsafe provider defaults")
	}
	t.Setenv("EVENT_SYNC_TIMEPAD_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TIMEPAD_TOKEN") {
		t.Fatal("explicit Timepad enable without token accepted")
	}
	t.Setenv("TIMEPAD_TOKEN", "fixture-secret")
	cfg, err = Load()
	if err != nil || !cfg.EventSyncTimepadEnabled || cfg.EventSyncKudaGoEnabled {
		t.Fatal("provider switches are not independent", err)
	}
}

func TestProviderSafetyRejectsMalformedAndUnboundedSettings(t *testing.T) {
	for name, values := range map[string][]string{
		"EVENT_SYNC_KUDAGO_ENABLED":       {"", "1", "TRUE", "maybe-secret"},
		"EVENT_SYNC_TIMEPAD_ENABLED":      {"", "yes"},
		"EVENT_SYNC_UPSERT_ONLY":          {"", "0"},
		"KUDAGO_MAX_REQUESTS_PER_MINUTE":  {"0", "31", "-1"},
		"TIMEPAD_MAX_REQUESTS_PER_MINUTE": {"0", "31"},
		"KUDAGO_MAX_PAGES_PER_RUN":        {"-1", "1001", ""},
		"TIMEPAD_MAX_PAGES_PER_RUN":       {"-1", "1001", ""},
		"EVENT_SYNC_MAX_RUN_DURATION":     {"0s", "3h", ""},
	} {
		for _, value := range values {
			t.Run(name+"="+value, func(t *testing.T) {
				setValidConfigEnv(t)
				t.Setenv(name, value)
				_, err := Load()
				if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "maybe-secret") {
					t.Fatalf("invalid setting error=%v", err)
				}
			})
		}
	}
}
