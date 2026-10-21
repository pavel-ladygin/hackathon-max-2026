package eventsources

import (
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
)

func TestManualAndScheduledSourceCityResolution(t *testing.T) {
	legacy := uuid.New()
	second := uuid.New()
	runner := &Runner{cityID: legacy, cities: sourceconfig.CityBindings{"generic:second": second}}
	if runner.CityForSource("generic:legacy") != legacy || runner.CityForSource(" generic:second ") != second {
		t.Fatal("source city resolution lost legacy default or explicit binding")
	}
}
