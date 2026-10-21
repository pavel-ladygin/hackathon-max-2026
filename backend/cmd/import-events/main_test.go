package main

import (
	"strings"
	"testing"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/kudago"
)

func TestRunRejectsUnsupportedArgumentsBeforeLoadingConfig(t *testing.T) {
	for name, args := range map[string][]string{
		"provider":        {"--provider=other", "--city=moscow"},
		"city":            {"--provider=kudago", "--city=other"},
		"oversized pages": {"--provider=timepad", "--city=moscow", "--max-pages=1001"},
		"negative pages":  {"--provider=timepad", "--city=moscow", "--max-pages=-1"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(args); err == nil || !strings.Contains(err.Error(), "must be") {
				t.Fatalf("run(%v) error = %v", args, err)
			}
		})
	}
}

func TestRunDisabledDoesNotRequireCommandConfiguration(t *testing.T) {
	t.Setenv("EVENT_SYNC_KUDAGO_ENABLED", "false")
	if err := run([]string{"--provider=kudago", "--city=moscow"}); err != nil {
		t.Fatalf("disabled command returned %v", err)
	}
}

func TestNewImporterSelectsConfiguredProvider(t *testing.T) {
	cfg := config.Config{
		KudaGoBaseURL: "https://kudago.com/public-api/v1.4", KudaGoTimeout: time.Second,
		KudaGoLocation: "msk", KudaGoPageSize: 100,
		TimepadBaseURL: "https://api.timepad.ru/v1", TimepadToken: "token",
		TimepadTimeout: time.Second, TimepadPageSize: 100, TimepadMaxRequestsPerMinute: 20,
	}
	for name, wantType := range map[string]any{"kudago": (*kudago.Client)(nil), "timepad": limitedTimepadImporter{}} {
		importer, err := newImporter(name, cfg, 1)
		if err != nil {
			t.Fatalf("newImporter(%q): %v", name, err)
		}
		switch wantType.(type) {
		case *kudago.Client:
			if _, ok := importer.(*kudago.Client); !ok {
				t.Fatalf("newImporter(%q) = %T, want *kudago.Client", name, importer)
			}
		case limitedTimepadImporter:
			limited, ok := importer.(limitedTimepadImporter)
			if !ok || limited.maxPages != 1 {
				t.Fatalf("newImporter(%q) = %#v, want limited Timepad importer", name, importer)
			}
		}
	}
}

func TestNewImporterRequiresTimepadToken(t *testing.T) {
	cfg := config.Config{TimepadBaseURL: "https://api.timepad.ru/v1", TimepadTimeout: time.Second, TimepadPageSize: 100}
	_, err := newImporter("timepad", cfg, 0)
	if err == nil || !strings.Contains(err.Error(), "TIMEPAD_TOKEN") {
		t.Fatalf("error = %v, want missing TIMEPAD_TOKEN", err)
	}
}

func TestCappedPagesNeverExceedsConfiguredCap(t *testing.T) {
	if got := cappedPages(50, 20); got != 20 {
		t.Fatalf("cappedPages(50, 20) = %d, want 20", got)
	}
	if got := cappedPages(10, 20); got != 10 {
		t.Fatalf("cappedPages(10, 20) = %d, want 10", got)
	}
}

func TestProviderPageCapUsesEnabledProviderConfiguration(t *testing.T) {
	cfg := config.Config{KudaGoMaxPagesPerRun: 7, TimepadMaxPagesPerRun: 3}
	if got := providerPageCap("kudago", cfg); got != 7 {
		t.Fatalf("KudaGo page cap = %d, want 7", got)
	}
	if got := providerPageCap("timepad", cfg); got != 3 {
		t.Fatalf("Timepad page cap = %d, want 3", got)
	}
}
