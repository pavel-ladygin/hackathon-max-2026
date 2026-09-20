package tickets

import "testing"

func TestParseAllowlistAndURLMatching(t *testing.T) {
	rules, err := parseAllowlist([]string{"tickets.example.test", "*.partner.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{allowlist: rules}
	for _, raw := range []string{
		"https://tickets.example.test/buy/1",
		"https://shop.partner.example.test/buy/1",
	} {
		if !service.isAllowedURL(raw) {
			t.Fatalf("URL %q should be allowed", raw)
		}
	}
	for _, raw := range []string{
		"http://tickets.example.test/buy/1",
		"https://tickets.example.test.attacker.test/buy/1",
		"https://partner.example.test/buy/1",
		"https://tickets.example.test:8443/buy/1",
		"https://user@tickets.example.test/buy/1",
		"https://127.0.0.1/buy/1",
		"not a URL",
	} {
		if service.isAllowedURL(raw) {
			t.Fatalf("URL %q should be denied", raw)
		}
	}
}

func TestParseAllowlistRejectsUnsafeRules(t *testing.T) {
	for _, values := range [][]string{nil, {""}, {"tickets.example.test:443"}, {"127.0.0.1"}, {"*."}, {"https://tickets.example.test"}} {
		if _, err := parseAllowlist(values); err == nil {
			t.Fatalf("allowlist %#v was accepted", values)
		}
	}
}
