package providers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validPersistenceEvent() NormalizedEvent {
	return NormalizedEvent{
		Source: "kudago", ExternalID: "42:1700000000", Title: "Event", Description: "Description",
		Venue:    NormalizedVenue{Name: "Venue", Address: "Address", Latitude: float64Pointer(55.75), Longitude: float64Pointer(37.61), VenueType: "other"},
		StartsAt: time.Unix(1_700_000_000, 0), Timezone: "Europe/Moscow", Currency: "RUB", Status: "published",
		Categories: []NormalizedCategory{{Slug: "concerts", Weight: 1, IsPrimary: true}},
	}
}

func float64Pointer(value float64) *float64 { return &value }

func TestProviderStableIDIsDeterministicAndNamespaced(t *testing.T) {
	first := providerStableID("event", "kudago\x0042:1700000000")
	if first != providerStableID("event", "kudago\x0042:1700000000") {
		t.Fatal("same provider identity produced different IDs")
	}
	if first == providerStableID("venue", "kudago\x0042:1700000000") || first == providerStableID("event", "other\x0042:1700000000") {
		t.Fatal("stable IDs are not separated by kind/provider")
	}
}

func TestRepositoryRejectsInvalidInputBeforeDatabase(t *testing.T) {
	cityID := uuid.New()
	for name, test := range map[string]struct {
		city   uuid.UUID
		mutate func(*NormalizedEvent)
	}{
		"missing city":        {uuid.Nil, func(*NormalizedEvent) {}},
		"demo source":         {cityID, func(event *NormalizedEvent) { event.Source = "demo" }},
		"user source":         {cityID, func(event *NormalizedEvent) { event.Source = "user" }},
		"demo flag":           {cityID, func(event *NormalizedEvent) { event.IsDemo = true }},
		"missing external ID": {cityID, func(event *NormalizedEvent) { event.ExternalID = "" }},
		"missing title":       {cityID, func(event *NormalizedEvent) { event.Title = "" }},
		"missing venue":       {cityID, func(event *NormalizedEvent) { event.Venue.Name = "" }},
		"missing start":       {cityID, func(event *NormalizedEvent) { event.StartsAt = time.Time{} }},
		"missing category":    {cityID, func(event *NormalizedEvent) { event.Categories = nil }},
	} {
		t.Run(name, func(t *testing.T) {
			event := validPersistenceEvent()
			test.mutate(&event)
			if err := validatePersistenceInput(test.city, event); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestKudaGoAuditCodesRemainPayloadFree(t *testing.T) {
	for _, code := range []string{"kudago_http_403", "kudago_http_429", "kudago_timeout", "kudago_transport_error"} {
		if got := safeSyncErrorText(code); got != code {
			t.Fatalf("code=%q got=%q", code, got)
		}
	}
	for _, unsafe := range []string{"kudago_http_403?token=private", "kudago_http_999", "<html>403 nginx</html>"} {
		if got := safeSyncErrorText(unsafe); got != "provider sync failed" {
			t.Fatalf("unsafe code accepted: %q", got)
		}
	}
}

func TestRepositoryRequiresDatabase(t *testing.T) {
	_, err := NewRepository(nil).Upsert(context.Background(), uuid.New(), validPersistenceEvent())
	if err == nil {
		t.Fatal("nil database accepted")
	}
}
