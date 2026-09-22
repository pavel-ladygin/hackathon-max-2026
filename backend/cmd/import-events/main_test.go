package main

import (
	"strings"
	"testing"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/kudago"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/timepad"
)

func TestRunRejectsUnsupportedArgumentsBeforeLoadingConfig(t *testing.T) {
	for name, args := range map[string][]string{
		"provider": {"--provider=other", "--city=moscow"},
		"city":     {"--provider=kudago", "--city=other"},
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
		TimepadTimeout: time.Second, TimepadPageSize: 100,
	}
	for name, wantType := range map[string]any{"kudago": (*kudago.Client)(nil), "timepad": (*timepad.Client)(nil)} {
		importer, err := newImporter(name, cfg)
		if err != nil {
			t.Fatalf("newImporter(%q): %v", name, err)
		}
		switch wantType.(type) {
		case *kudago.Client:
			if _, ok := importer.(*kudago.Client); !ok {
				t.Fatalf("newImporter(%q) = %T, want *kudago.Client", name, importer)
			}
		case *timepad.Client:
			if _, ok := importer.(*timepad.Client); !ok {
				t.Fatalf("newImporter(%q) = %T, want *timepad.Client", name, importer)
			}
		}
	}
}

func TestNewImporterRequiresTimepadToken(t *testing.T) {
	cfg := config.Config{TimepadBaseURL: "https://timepad.example/v1", TimepadTimeout: time.Second, TimepadPageSize: 100}
	_, err := newImporter("timepad", cfg)
	if err == nil || !strings.Contains(err.Error(), "TIMEPAD_TOKEN") {
		t.Fatalf("error = %v, want missing TIMEPAD_TOKEN", err)
	}
}
