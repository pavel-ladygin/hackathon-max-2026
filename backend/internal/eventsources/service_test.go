package eventsources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/generic"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/resourcedomains"
)

func validSourceInput() sourceconfig.Input {
	return sourceconfig.Input{
		SourceKey: "generic:service-test", Name: "Service test", Enabled: true,
		EndpointURL: "https://events.example.test/api", AuthType: "none",
		Pagination: json.RawMessage(`{"mode":"none"}`),
		Mapping:    json.RawMessage(`{"external_id":{"path":"id"},"title":{"path":"name"},"starts_at":{"path":"date"},"venue_name":{"path":"place.name"}}`),
		Defaults:   sourceconfig.Defaults{Category: "other", Timezone: "Europe/Moscow", Currency: "RUB", Status: "published"},
		PriceUnit:  "major",
	}
}

func TestValidateInput(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*sourceconfig.Input)
		wantErr bool
	}{
		{name: "valid", mutate: func(*sourceconfig.Input) {}},
		{name: "source key namespace", mutate: func(in *sourceconfig.Input) { in.SourceKey = "kudago" }, wantErr: true},
		{name: "missing required mapping", mutate: func(in *sourceconfig.Input) { in.Mapping = json.RawMessage(`{"title":{"path":"name"}}`) }, wantErr: true},
		{name: "unsupported pagination", mutate: func(in *sourceconfig.Input) { in.Pagination = json.RawMessage(`{"mode":"cursor"}`) }, wantErr: true},
		{name: "private endpoint", mutate: func(in *sourceconfig.Input) { in.EndpointURL = "https://127.0.0.1/events" }, wantErr: true},
		{name: "invalid price unit", mutate: func(in *sourceconfig.Input) { in.PriceUnit = "decimal" }, wantErr: true},
		{name: "invalid timezone", mutate: func(in *sourceconfig.Input) { in.Defaults.Timezone = "Mars/Olympus_Mons" }, wantErr: true},
		{name: "invalid status", mutate: func(in *sourceconfig.Input) { in.Defaults.Status = "upcoming" }, wantErr: true},
		{name: "unknown category", mutate: func(in *sourceconfig.Input) { in.Defaults.Category = "custom-category" }, wantErr: true},
		{name: "unknown mapping field", mutate: func(in *sourceconfig.Input) {
			in.Mapping = json.RawMessage(`{"external_id":{"path":"id"},"title":{"path":"name"},"starts_at":{"path":"date"},"venue_name":{"path":"place.name"},"district":{"path":"place.district"}}`)
		}, wantErr: true},
		{name: "unsupported transform", mutate: func(in *sourceconfig.Input) {
			in.Mapping = json.RawMessage(`{"external_id":{"path":"id"},"title":{"path":"name","transform":"expression"},"starts_at":{"path":"date"},"venue_name":{"path":"place.name"}}`)
		}, wantErr: true},
		{name: "incompatible transform", mutate: func(in *sourceconfig.Input) {
			in.Mapping = json.RawMessage(`{"external_id":{"path":"id"},"title":{"path":"name","transform":"number"},"starts_at":{"path":"date"},"venue_name":{"path":"place.name"}}`)
		}, wantErr: true},
		{name: "invalid mapping path", mutate: func(in *sourceconfig.Input) {
			in.Mapping = json.RawMessage(`{"external_id":{"path":"id"},"title":{"path":"name"},"starts_at":{"path":"date..start"},"venue_name":{"path":"place.name"}}`)
		}, wantErr: true},
		{name: "unsupported legacy transform config", mutate: func(in *sourceconfig.Input) { in.TransformConfig = json.RawMessage(`{"title":"strip_html"}`) }, wantErr: true},
		{name: "query auth conflicts with pagination", mutate: func(in *sourceconfig.Input) {
			in.AuthType = "api_key_query"
			in.AuthName = "page"
			in.Secret = "secret"
			in.Pagination = json.RawMessage(`{"mode":"page","page_size":10}`)
		}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validSourceInput()
			test.mutate(&input)
			err := ValidateInput(input)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateInput error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestPreviewGroupsUnapprovedMediaHostsAsWarnings(t *testing.T) {
	input := validSourceInput()
	input.Mapping = json.RawMessage(`{"external_id":{"path":"id"},"title":{"path":"title"},"starts_at":{"path":"starts_at"},"venue_name":{"path":"venue"},"image":{"path":"image"},"ticket_url":{"path":"ticket"}}`)
	result, err := previewWithFetcher(context.Background(), input, "", func(context.Context, generic.HTTPConfig, generic.PaginationConfig) ([]any, bool, error) {
		return []any{
			map[string]any{"id": "1", "title": "One", "starts_at": "2026-10-03T19:00:00+03:00", "venue": "Hall", "image": "https://unapproved.example/poster.jpg?access_token=secret", "ticket": "https://tickets.unapproved.example/event?key=secret"},
			map[string]any{"id": "2", "title": "Two", "starts_at": "2026-10-04T19:00:00+03:00", "venue": "Hall", "image": "https://unapproved.example/second.jpg", "ticket": "https://tickets.unapproved.example/other"},
			map[string]any{"id": "3", "title": "Three", "starts_at": "2026-10-05T19:00:00+03:00", "venue": "Hall", "image": "http://bad.example/poster.jpg", "ticket": "javascript:alert(1)"},
		}, true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid != 3 || result.Invalid != 0 {
		t.Fatalf("preview counts = valid %d invalid %d, want 3 and 0", result.Valid, result.Invalid)
	}
	warnings := map[string]int{}
	for _, warning := range result.Warnings {
		warnings[warning.Code] = warning.Count
	}
	if warnings["image_host_not_allowed"] != 2 || warnings["ticket_host_not_allowed"] != 2 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(result.ResourceDomains) != 2 {
		t.Fatalf("resource domains = %#v, want image and ticket hosts", result.ResourceDomains)
	}
	if result.ResourceDomains[0].Hostname != "unapproved.example" || result.ResourceDomains[0].Purpose != resourcedomains.PurposeImage || result.ResourceDomains[0].Count != 2 || result.ResourceDomains[0].Approved {
		t.Fatalf("image resource domain = %+v", result.ResourceDomains[0])
	}
	if result.ResourceDomains[1].Hostname != "tickets.unapproved.example" || result.ResourceDomains[1].Purpose != resourcedomains.PurposeTicket || result.ResourceDomains[1].Count != 2 || result.ResourceDomains[1].Approved {
		t.Fatalf("ticket resource domain = %+v", result.ResourceDomains[1])
	}
	encoded, _ := json.Marshal(result.ResourceDomains)
	for _, sensitive := range []string{"token", "secret", "poster.jpg", "tickets.unapproved.example/event"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Errorf("resource domains disclosed %q: %s", sensitive, encoded)
		}
	}
	applyApprovedDomains(&result, func(hostname string, purpose resourcedomains.Purpose) (bool, error) {
		return hostname == "unapproved.example" && purpose == resourcedomains.PurposeImage, nil
	})
	if !result.ResourceDomains[0].Approved || result.ResourceDomains[1].Approved {
		t.Fatalf("approval was not scoped by host and purpose: %+v", result.ResourceDomains)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "ticket_host_not_allowed" || result.Warnings[0].Count != 2 {
		t.Fatalf("warnings after simulated source approval = %+v", result.Warnings)
	}
}

func TestPreviewRejectsInvalidConfigBeforeFetching(t *testing.T) {
	input := validSourceInput()
	input.EndpointURL = "https://127.0.0.1/events"
	if _, err := Preview(context.Background(), input, ""); err == nil {
		t.Fatal("Preview accepted a private endpoint")
	}
	// Preview has no repository/database dependency. Invalid configurations are
	// rejected before it reaches the generic HTTP traversal.
}
