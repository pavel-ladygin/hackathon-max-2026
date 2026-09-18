package catalog

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

func TestAvailabilityFromRowPreservesCatalogFacts(t *testing.T) {
	priceFrom, priceTo := int32(0), int32(12500)
	ticketURL := "https://tickets.example/free"

	tests := []struct {
		name            string
		status          string
		ticketAvailable bool
		priceFrom       pgtype.Int4
		priceTo         pgtype.Int4
		currency        string
		ticketURL       pgtype.Text
		wantFrom        *int32
		wantTo          *int32
		wantURL         *string
	}{
		{name: "published available", status: "published", ticketAvailable: true, priceFrom: pgtype.Int4{Int32: 1000, Valid: true}, priceTo: pgtype.Int4{Int32: 2500, Valid: true}, currency: "RUB", ticketURL: pgtype.Text{String: "https://tickets.example/event", Valid: true}, wantFrom: ptr(int32(1000)), wantTo: ptr(int32(2500)), wantURL: ptr("https://tickets.example/event")},
		{name: "sold out retains raw facts", status: "sold_out", ticketAvailable: true, priceFrom: pgtype.Int4{Int32: 100, Valid: true}, currency: "RUB", ticketURL: pgtype.Text{String: "https://tickets.example/sold", Valid: true}, wantFrom: ptr(int32(100)), wantURL: ptr("https://tickets.example/sold")},
		{name: "cancelled unavailable", status: "cancelled", ticketAvailable: false, currency: "RUB"},
		{name: "ticket unavailable with URL", status: "published", ticketAvailable: false, priceFrom: pgtype.Int4{Int32: 4000, Valid: true}, ticketURL: pgtype.Text{String: "https://tickets.example/closed", Valid: true}, wantFrom: ptr(int32(4000)), wantURL: ptr("https://tickets.example/closed")},
		{name: "free zero", status: "published", ticketAvailable: true, priceFrom: pgtype.Int4{Int32: priceFrom, Valid: true}, priceTo: pgtype.Int4{Int32: priceTo, Valid: true}, currency: "RUB", ticketURL: pgtype.Text{String: ticketURL, Valid: true}, wantFrom: &priceFrom, wantTo: &priceTo, wantURL: &ticketURL},
		{name: "nullable prices and URL", status: "published", ticketAvailable: true, currency: "RUB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := availabilityFromRow(platform.GetEventAvailabilityRow{Status: tt.status, PriceFromMinor: tt.priceFrom, PriceToMinor: tt.priceTo, Currency: tt.currency, TicketAvailable: tt.ticketAvailable, TicketUrl: tt.ticketURL})
			if !got.Exists || got.Status != tt.status || got.Currency != tt.currency || got.TicketAvailable != tt.ticketAvailable {
				t.Fatalf("raw availability fields = %+v", got)
			}
			assertOptionalInt(t, "price from", got.PriceFromMinor, tt.wantFrom)
			assertOptionalInt(t, "price to", got.PriceToMinor, tt.wantTo)
			if !optionalStringEqual(got.TicketURL, tt.wantURL) {
				t.Fatalf("ticket URL = %v, want %v", got.TicketURL, tt.wantURL)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func assertOptionalInt(t *testing.T, field string, got, want *int32) {
	t.Helper()
	if (got == nil) != (want == nil) || got != nil && *got != *want {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

func optionalStringEqual(got, want *string) bool {
	return (got == nil && want == nil) || (got != nil && want != nil && *got == *want)
}
