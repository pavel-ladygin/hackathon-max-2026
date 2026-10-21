package eventsubmissions

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestValidateSubmissionPriceAndResources(t *testing.T) {
	tests := []struct {
		name    string
		input   SubmissionInput
		wantErr bool
	}{
		{name: "free normalizes to zero", input: SubmissionInput{Title: "Show", Description: "Details", CityID: validCityID(), VenueName: "Hall", VenueAddress: "Street 1", PriceMode: "free", CategorySlug: "concerts"}},
		{name: "free rejects price", input: SubmissionInput{Title: "Show", Description: "Details", CityID: validCityID(), VenueName: "Hall", VenueAddress: "Street 1", PriceMode: "free", PriceFromMinor: int32Ptr(1), CategorySlug: "concerts"}, wantErr: true},
		{name: "unknown rejects price", input: SubmissionInput{Title: "Show", Description: "Details", CityID: validCityID(), VenueName: "Hall", VenueAddress: "Street 1", PriceMode: "unknown", PriceFromMinor: int32Ptr(0), TicketURL: strPtr("https://tickets.example.test"), CategorySlug: "concerts"}, wantErr: true},
		{name: "paid needs ticket", input: SubmissionInput{Title: "Show", Description: "Details", CityID: validCityID(), VenueName: "Hall", VenueAddress: "Street 1", PriceMode: "paid", PriceFromMinor: int32Ptr(100), CategorySlug: "concerts"}, wantErr: true},
		{name: "non https ticket rejected", input: SubmissionInput{Title: "Show", Description: "Details", CityID: validCityID(), VenueName: "Hall", VenueAddress: "Street 1", PriceMode: "free", TicketURL: strPtr("http://tickets.example.test"), CategorySlug: "concerts"}, wantErr: true},
		{name: "private image rejected", input: SubmissionInput{Title: "Show", Description: "Details", CityID: validCityID(), VenueName: "Hall", VenueAddress: "Street 1", PriceMode: "free", ImageURL: strPtr("https://127.0.0.1/p.png"), CategorySlug: "concerts"}, wantErr: true},
		{name: "unknown category rejected", input: SubmissionInput{Title: "Show", Description: "Details", CityID: validCityID(), VenueName: "Hall", VenueAddress: "Street 1", PriceMode: "free", CategorySlug: "invented"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateInput(&test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateInput error=%v, wantErr=%t", err, test.wantErr)
			}
			if err == nil && test.input.PriceMode == "free" && (test.input.PriceFromMinor == nil || *test.input.PriceFromMinor != 0 || test.input.PriceToMinor == nil || *test.input.PriceToMinor != 0) {
				t.Fatalf("free price not canonicalized: from=%v to=%v", test.input.PriceFromMinor, test.input.PriceToMinor)
			}
		})
	}
}

func TestValidateSubmissionLimitsAndRequiredTicket(t *testing.T) {
	base := SubmissionInput{Title: "Show", Description: "Details", CityID: validCityID(), VenueName: "Hall", VenueAddress: "Street 1", PriceMode: "paid", PriceFromMinor: int32Ptr(maxPriceMinor + 1), TicketURL: strPtr("https://tickets.example.test"), CategorySlug: "concerts"}
	if err := validateInput(&base); err == nil {
		t.Fatal("price above project cap was accepted")
	}
	base.PriceFromMinor = int32Ptr(100)
	if err := validateInput(&base); err != nil {
		t.Fatalf("valid paid submission rejected: %v", err)
	}
	base.Title = string(make([]rune, 201))
	if err := validateInput(&base); err == nil {
		t.Fatal("title over 200 characters was accepted")
	}
}

func validCityID() uuid.UUID      { return uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa") }
func int32Ptr(value int32) *int32 { return &value }
func strPtr(value string) *string { return &value }

func TestModerationMutationsRequireSameOriginAndMarker(t *testing.T) {
	for _, tc := range []struct {
		name, origin, marker string
		allowed              bool
	}{
		{"missing origin and marker", "", "", false},
		{"marker alone", "", "1", false},
		{"origin alone", "https://admin.example.test", "", false},
		{"cross origin", "https://attacker.example.test", "1", false},
		{"insecure external origin", "http://admin.example.test", "1", false},
		{"origin with path", "https://admin.example.test/path", "1", false},
		{"valid same origin", "https://admin.example.test", "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://admin.example.test/api/v1/internal/event-submissions/id/approve", nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("X-Admin-Request", tc.marker)
			response := httptest.NewRecorder()
			if got := requireMutation(response, req); got != tc.allowed {
				t.Fatalf("allowed=%t want%t response=%s", got, tc.allowed, response.Body.String())
			}
			if !tc.allowed && response.Code != http.StatusForbidden {
				t.Fatalf("denial status=%d", response.Code)
			}
		})
	}
}
