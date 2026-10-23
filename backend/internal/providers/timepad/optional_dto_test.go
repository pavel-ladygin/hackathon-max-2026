package timepad

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func TestMalformedOptionalMetadataDoesNotAbortPage(t *testing.T) {
	for name, field := range map[string]string{
		"created_at":             `"created_at":{}`,
		"ends_at":                `"ends_at":[]`,
		"description_short":      `"description_short":false`,
		"url":                    `"url":42`,
		"poster_image shape":     `"poster_image":[]`,
		"poster_image URL":       `"poster_image":{"default_url":{}}`,
		"organization shape":     `"organization":false`,
		"organization name":      `"organization":{"name":[]}`,
		"organization unused ID": `"organization":{"id":{},"name":"Organizer"}`,
		"location metadata":      `"location":{"city":"Санкт-Петербург","country":{},"address":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"total":2,"values":[{"id":1,"name":"Kept","starts_at":"2026-10-01T07:00:00Z",%s},{"id":2,"name":"Good","starts_at":"2026-10-01T07:00:00Z"}]}`, field)
			var page eventsPage
			if err := json.Unmarshal([]byte(raw), &page); err != nil {
				t.Fatalf("optional metadata aborted page: %v", err)
			}
			if len(page.Values) != 2 {
				t.Fatalf("page lost events: %+v", page)
			}
			for _, event := range page.Values {
				normalized, ok := normalizeEvent(event)
				if !ok || normalized.ExternalID != fmt.Sprint(event.ID) || normalized.Title != event.Name {
					t.Fatalf("valid event lost: %+v, accepted=%t", normalized, ok)
				}
				if normalized.PublishedAt != nil || normalized.EndsAt != nil || normalized.Description != "" || normalized.TicketURL != nil || normalized.TicketAvailable || len(normalized.Images) != 0 || normalized.Venue.Address != "" || normalized.PriceFromMinor != nil || normalized.PriceToMinor != nil {
					t.Fatalf("malformed metadata produced data: %+v", normalized)
				}
				if name == "organization unused ID" && event.ID == 1 && normalized.Venue.Name != "Organizer" {
					t.Fatalf("unused malformed ID discarded valid organizer name: %+v", normalized.Venue)
				}
			}
		})
	}
}

func TestNonstandardTicketsDoNotBypassMandatoryDateValidation(t *testing.T) {
	for _, start := range []string{`null`, `""`, `"not-a-date"`, `"2026-02-30T07:00:00Z"`, `"2026-10-01T07:00:00"`} {
		t.Run(start, func(t *testing.T) {
			var event eventDTO
			raw := fmt.Sprintf(`{"id":1,"name":"Event","starts_at":%s,"ticket_types":{},"created_at":{},"ends_at":[]}`, start)
			if err := json.Unmarshal([]byte(raw), &event); err != nil {
				t.Fatal(err)
			}
			_, reason := normalizeEventWithReason(event)
			if reason != providers.RejectionMissingStartsAt && reason != providers.RejectionInvalidStartsAt {
				t.Fatalf("unreliable start was not rejected: %q", reason)
			}
		})
	}
}

func TestNonstandardTicketsPreserveEligibilityAndRegistrationChecks(t *testing.T) {
	for name, test := range map[string]struct {
		fields      string
		wantActive  bool
		wantTickets bool
	}{
		"unknown access":      {`"access_status":"future_status","moderation_status":"shown"`, false, true},
		"unknown moderation":  {`"access_status":"public","moderation_status":"future_status"`, false, true},
		"hidden":              {`"access_status":"private","moderation_status":"hidden"`, false, true},
		"public":              {`"access_status":"public","moderation_status":"shown"`, true, true},
		"malformed open flag": {`"access_status":"public","moderation_status":"shown","registration_data":{"price_min":100,"price_max":200,"is_registration_open":"true"}`, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			var event eventDTO
			registration := `"registration_data":{"price_min":100,"price_max":200,"is_registration_open":true},`
			if name == "malformed open flag" {
				registration = ""
			}
			raw := `{"id":1,"name":"Event","starts_at":"2026-10-01T07:00:00Z","url":"https://timepad.ru/event/1","ticket_types":{},` + registration + test.fields + `}`
			if err := json.Unmarshal([]byte(raw), &event); err != nil {
				t.Fatal(err)
			}
			normalized, ok := normalizeEvent(event)
			if !ok || normalized.ProviderActive != test.wantActive || normalized.TicketAvailable != test.wantTickets || normalized.Status != providers.EventStatusPublished {
				t.Fatalf("eligibility changed: %+v, accepted=%t", normalized, ok)
			}
			if normalized.PriceFromMinor == nil || *normalized.PriceFromMinor != 10000 || normalized.PriceToMinor == nil || *normalized.PriceToMinor != 20000 {
				t.Fatalf("unrelated metadata discarded valid registration prices: %+v", normalized)
			}
		})
	}
}

func TestMandatoryAndEligibilityTypesRemainStrict(t *testing.T) {
	for name, raw := range map[string]string{
		"id":                `{"id":{},"name":"Event","starts_at":"2026-10-01T07:00:00Z"}`,
		"name":              `{"id":1,"name":[],"starts_at":"2026-10-01T07:00:00Z"}`,
		"starts_at":         `{"id":1,"name":"Event","starts_at":false}`,
		"location":          `{"id":1,"name":"Event","starts_at":"2026-10-01T07:00:00Z","location":[]}`,
		"city":              `{"id":1,"name":"Event","starts_at":"2026-10-01T07:00:00Z","location":{"city":{}}}`,
		"access_status":     `{"id":1,"name":"Event","starts_at":"2026-10-01T07:00:00Z","access_status":[]}`,
		"moderation_status": `{"id":1,"name":"Event","starts_at":"2026-10-01T07:00:00Z","moderation_status":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			var event eventDTO
			if err := json.Unmarshal([]byte(raw), &event); err == nil {
				t.Fatalf("invalid %s type silently accepted", name)
			}
		})
	}
	for _, raw := range []string{
		`{"total":{},"values":[]}`,
		`{"total":1,"values":{}}`,
		`{"total":1,"values":[{"id":1,"ticket_types":[}]}`,
	} {
		var page eventsPage
		if err := json.Unmarshal([]byte(raw), &page); err == nil {
			t.Fatalf("invalid envelope/JSON silently accepted: %s", raw)
		}
	}
}
