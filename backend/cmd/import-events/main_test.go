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
		"provider":         {"--provider=other", "--city=moscow"},
		"city":             {"--provider=kudago", "--city=other"},
		"negative pages":   {"--provider=timepad", "--city=moscow", "--max-pages=-1"},
		"pages for kudago": {"--provider=kudago", "--city=moscow", "--max-pages=1"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(args); err == nil || !strings.Contains(err.Error(), "must be") {
				t.Fatalf("run(%v) error = %v", args, err)
			}
		})
	}
}

func TestNewImporterSelectsConfiguredProvider(t *testing.T) {
	cfg := config.Config{
		KudaGoBaseURL: "https://kudago.example", KudaGoTimeout: time.Second,
		KudaGoLocation: "msk", KudaGoPageSize: 100,
		TimepadBaseURL: "https://timepad.example/v1", TimepadToken: "token",
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
	cfg := config.Config{TimepadBaseURL: "https://timepad.example/v1", TimepadTimeout: time.Second, TimepadPageSize: 100}
	_, err := newImporter("timepad", cfg, 0)
	if err == nil || !strings.Contains(err.Error(), "TIMEPAD_TOKEN") {
		t.Fatalf("error = %v, want missing TIMEPAD_TOKEN", err)
	}
}
