package main

import (
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
)

func TestGenericSyncProvidersIncludeOnlyEnabledSourcesInStableOrder(t *testing.T) {
	firstID, secondID, disabledID := uuid.New(), uuid.New(), uuid.New()
	sources := []sourceconfig.Source{
		{ID: secondID, SourceKey: "generic:zeta", Name: "Zeta", Enabled: true},
		{ID: disabledID, SourceKey: "generic:disabled", Name: "Disabled", Enabled: false},
		{ID: firstID, SourceKey: "generic:alpha", Name: "Alpha", Enabled: true},
	}
	providers := genericSyncProviders(sources, nil)
	if len(providers) != 2 {
		t.Fatalf("scheduled generic sources=%d, want 2", len(providers))
	}
	if providers[0].name != "generic:alpha" || providers[1].name != "generic:zeta" {
		t.Fatalf("provider order = %q, %q", providers[0].name, providers[1].name)
	}
	if providers[0].priority != genericPriority || providers[1].priority != genericPriority {
		t.Fatalf("generic priorities = %d, %d", providers[0].priority, providers[1].priority)
	}
}
