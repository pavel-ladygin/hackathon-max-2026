package resourcedomains

import "testing"

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		input, want string
		valid       bool
	}{
		{"CDN.Partner.RU", "cdn.partner.ru", true},
		{"cdn.partner.ru.", "cdn.partner.ru", true},
		{"*.partner.ru", "", false},
		{"https://cdn.partner.ru/a", "", false},
		{"cdn.partner.ru/path", "", false},
		{"evil-example.ru", "evil-example.ru", true},
		{"127.0.0.1", "", false},
		{"127.1", "", false},
		{"0x7f.0x0.0x0.0x1", "", false},
		{"0x7f.0.0.1", "", false},
		{"[::1]", "", false},
		{"localhost", "", false},
		{"x.localhost", "", false},
		{"metadata.internal", "", false},
		{"db.private", "", false},
		{"printer.lan", "", false},
		{"bad..example.com", "", false},
		{"-bad.example.com", "", false},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := NormalizeHostname(test.input)
			if (err == nil) != test.valid || got != test.want {
				t.Fatalf("NormalizeHostname(%q) = %q, %v; want %q valid=%t", test.input, got, err, test.want, test.valid)
			}
		})
	}
}

func TestResourceHostname(t *testing.T) {
	tests := []struct {
		input, want string
		valid       bool
	}{
		{"https://CDN.Partner.RU/a.jpg?token=secret", "cdn.partner.ru", true},
		{"https://cdn.partner.ru:443/a.jpg", "cdn.partner.ru", true},
		{"http://cdn.partner.ru/a.jpg", "", false},
		{"https://user@cdn.partner.ru/a.jpg", "", false},
		{"https://cdn.partner.ru:8443/a.jpg", "", false},
		{"https://127.0.0.1/a.jpg", "", false},
		{"https://cdn.partner.ru/a.jpg#fragment", "", false},
		{"not a url", "", false},
		{"https://cdn.partner.ru:/a.jpg", "", false},
		{"https://cdn.partner.ru/a.jpg#", "", false},
		{" https://cdn.partner.ru/a.jpg", "", false},
		{"https://cdn.partner.ru/a.jpg\n", "", false},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ResourceHostname(test.input)
			if (err == nil) != test.valid || got != test.want {
				t.Fatalf("ResourceHostname(%q) = %q, %v; want %q valid=%t", test.input, got, err, test.want, test.valid)
			}
		})
	}
}

func TestValidPurpose(t *testing.T) {
	for _, test := range []struct {
		purpose Purpose
		want    bool
	}{{PurposeImage, true}, {PurposeTicket, true}, {"api", false}, {"", false}} {
		if got := ValidPurpose(test.purpose); got != test.want {
			t.Errorf("ValidPurpose(%q)=%t, want %t", test.purpose, got, test.want)
		}
	}
}
