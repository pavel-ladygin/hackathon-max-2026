package timepad

import (
	"encoding/json"
	"testing"
)

func TestTicketTypesDTOUnmarshalOptionalShapes(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []ticketTypeDTO
		wantNil bool
	}{
		{
			name: "usable tickets keep zero price",
			raw:  `[{"price":0,"is_active":true},{"price":125.5,"is_active":false}]`,
			want: []ticketTypeDTO{{Price: 0, IsActive: true}},
		},
		{name: "null", raw: `null`, wantNil: true},
		{name: "empty array", raw: `[]`, wantNil: true},
		{name: "object", raw: `{}`, wantNil: true},
		{name: "values wrapper is ignored", raw: `{"values":[{"price":99,"is_active":true}]}`, wantNil: true},
		{name: "scalar", raw: `12`, wantNil: true},
		{
			name: "inactive malformed price and unused remaining ignored",
			raw:  `[{"price":"bad","is_active":false},{"price":200,"is_active":true,"remaining":{"invalid":true}}]`,
			want: []ticketTypeDTO{{Price: 200, IsActive: true}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var tickets ticketTypesDTO
			if err := json.Unmarshal([]byte(test.raw), &tickets); err != nil {
				t.Fatal(err)
			}
			if (tickets == nil) != test.wantNil {
				t.Fatalf("tickets nil = %t, want %t", tickets == nil, test.wantNil)
			}
			if len(tickets) != len(test.want) {
				t.Fatalf("tickets = %#v, want %#v", tickets, test.want)
			}
			for i := range test.want {
				if tickets[i] != test.want[i] {
					t.Fatalf("tickets = %#v, want %#v", tickets, test.want)
				}
			}
		})
	}
}

func TestRegistrationDataDTOOptionalShapes(t *testing.T) {
	for _, raw := range []string{
		`null`,
		`[]`,
		`12`,
		`{"values":{"price_min":1,"price_max":2}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var event eventDTO
			if err := json.Unmarshal([]byte(`{"registration_data":`+raw+`}`), &event); err != nil {
				t.Fatal(err)
			}
			if event.RegistrationData.PriceMin != nil || event.RegistrationData.PriceMax != nil || event.RegistrationData.IsRegistrationOpen {
				t.Fatalf("registration data = %#v, want absent", event.RegistrationData)
			}
		})
	}
}

func TestNormalizePricesWithOptionalTimepadData(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantFrom *int32
		wantTo   *int32
	}{
		{
			name:  "malformed active ticket makes the entire ticket range unknown",
			input: `{"ticket_types":[{"price":"10","is_active":true},{"price":200,"is_active":true}]}`,
		},
		{
			name:  "missing active price cannot establish free from another ticket",
			input: `{"ticket_types":[{"price":0,"is_active":true},{"is_active":true}]}`,
		},
		{
			name:     "complete registration survives incomplete ticket data",
			input:    `{"registration_data":{"price_min":100,"price_max":500},"ticket_types":[{"price":"10","is_active":true},{"price":0,"is_active":true}]}`,
			wantFrom: minorPointer(10000),
			wantTo:   minorPointer(50000),
		},
		{
			name:     "explicit active free ticket is known zero",
			input:    `{"ticket_types":[{"price":0,"is_active":true}]}`,
			wantFrom: minorPointer(0),
			wantTo:   minorPointer(0),
		},
		{
			name:     "paid tickets compute unsorted min and max",
			input:    `{"ticket_types":[{"price":300,"is_active":true},{"price":50,"is_active":true},{"price":150,"is_active":true},{"price":0,"is_active":false}]}`,
			wantFrom: minorPointer(5000),
			wantTo:   minorPointer(30000),
		},
		{
			name:  "inactive and invalid tickets do not establish a price",
			input: `{"ticket_types":[{"price":0,"is_active":false},{"price":-1,"is_active":true},{"price":0,"is_active":null}]}`,
		},
		{
			name:  "missing minimum does not invent a free lower bound",
			input: `{"registration_data":{"price_max":500}}`,
		},
		{
			name:  "null maximum with zero minimum does not imply free",
			input: `{"registration_data":{"price_min":0,"price_max":null}}`,
		},
		{
			name:     "incomplete registration falls back to reliable tickets",
			input:    `{"registration_data":{"price_min":100},"ticket_types":[{"price":200,"is_active":true}]}`,
			wantFrom: minorPointer(20000),
			wantTo:   minorPointer(20000),
		},
		{
			name:  "missing and null ticket prices do not become free",
			input: `{"ticket_types":[{"is_active":true},{"price":null,"is_active":true}]}`,
		},
		{
			name:     "malformed registration fields fall back to tickets",
			input:    `{"registration_data":{"price_min":"wrong","price_max":300,"is_registration_open":"yes"},"ticket_types":[{"price":0,"is_active":true},{"price":123.456,"is_active":true},{"price":-1,"is_active":true},{"price":200,"is_active":false}]}`,
			wantFrom: minorPointer(0),
			wantTo:   minorPointer(12346),
		},
		{
			name:     "registration wrong top shape falls back to tickets",
			input:    `{"registration_data":{"values":{"price_min":1,"price_max":2}},"ticket_types":[{"price":99,"is_active":true}]}`,
			wantFrom: minorPointer(9900),
			wantTo:   minorPointer(9900),
		},
		{
			name:     "complete registration range takes precedence",
			input:    `{"registration_data":{"price_min":500,"price_max":750},"ticket_types":[{"price":0,"is_active":true},{"price":1000,"is_active":true}]}`,
			wantFrom: minorPointer(50000),
			wantTo:   minorPointer(75000),
		},
		{
			name:     "zero registration range falls back to paid tickets",
			input:    `{"registration_data":{"price_min":0,"price_max":0},"ticket_types":[{"price":125,"is_active":true}]}`,
			wantFrom: minorPointer(12500),
			wantTo:   minorPointer(12500),
		},
		{
			name:  "zero registration range without tickets is absent",
			input: `{"registration_data":{"price_min":0,"price_max":0}}`,
		},
		{
			name:  "reversed registration range rejected",
			input: `{"registration_data":{"price_min":750,"price_max":500},"ticket_types":[{"price":100,"is_active":true}]}`,
		},
		{
			name:  "overflow registration range rejected",
			input: `{"registration_data":{"price_min":1,"price_max":21474836.48}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var event eventDTO
			if err := json.Unmarshal([]byte(test.input), &event); err != nil {
				t.Fatal(err)
			}
			from, to := normalizePrices(event)
			if !sameMinor(from, test.wantFrom) || !sameMinor(to, test.wantTo) {
				t.Fatalf("prices = %v..%v, want %v..%v", from, to, test.wantFrom, test.wantTo)
			}
		})
	}
}

func minorPointer(value int32) *int32 {
	return &value
}

func sameMinor(got, want *int32) bool {
	if got == nil || want == nil {
		return got == want
	}
	return *got == *want
}

func TestTicketTypesDTORejectsInvalidJSON(t *testing.T) {
	var tickets ticketTypesDTO
	if err := json.Unmarshal([]byte(`[{"price":1}`), &tickets); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestIncompleteTicketSetDoesNotPublishPartialRange(t *testing.T) {
	for _, badTicket := range []string{
		`null`, `4`, `[]`, `{}`, `{"price":10}`, `{"price":10,"is_active":null}`,
		`{"price":10,"is_active":"true"}`, `{"price":null,"is_active":true}`,
		`{"price":"10","is_active":true}`, `{"is_active":true}`,
	} {
		t.Run(badTicket, func(t *testing.T) {
			var event eventDTO
			raw := `{"ticket_types":[{"price":200,"is_active":true},` + badTicket + `]}`
			if err := json.Unmarshal([]byte(raw), &event); err != nil {
				t.Fatal(err)
			}
			from, to := normalizePrices(event)
			if from != nil || to != nil {
				t.Fatalf("incomplete set published a partial range: %v..%v", from, to)
			}
		})
	}
}
