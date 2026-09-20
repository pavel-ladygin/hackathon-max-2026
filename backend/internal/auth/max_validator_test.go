package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signedInitData(t *testing.T, botToken string, values map[string]string) string {
	t.Helper()
	parts := make([]string, 0, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	// Keep the helper's signed payload in the same canonical order as MAX.
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	derived := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = derived.Write([]byte(botToken))
	mac := hmac.New(sha256.New, derived.Sum(nil))
	_, _ = mac.Write([]byte(strings.Join(parts, "\n")))
	encoded := make([]string, 0, len(values)+1)
	for _, key := range keys {
		encoded = append(encoded, key+"="+url.PathEscape(values[key]))
	}
	encoded = append(encoded, "hash="+hex.EncodeToString(mac.Sum(nil)))
	return strings.Join(encoded, "&")
}

func validValues(now time.Time) map[string]string {
	return map[string]string{
		"auth_date":   now.Format("-"), // overwritten by each test
		"user":        `{"id":42,"first_name":"Ada","last_name":"Lovelace","language_code":"en"}`,
		"start_param": "invite+code",
	}
}

func TestMAXValidatorOfficialSignatureAndLiteralPlus(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	values := validValues(now)
	values["auth_date"] = "1700000000"
	raw := signedInitData(t, "test-bot-secret", values)
	v, err := newMAXValidator("test-bot-secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.validate(raw, now)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if identity.maxUserID != 42 || identity.startParam != "invite+code" || identity.locale != "en" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestMAXValidatorRejectsTamperingMalformedAndDuplicateInput(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	values := validValues(now)
	values["auth_date"] = "1700000000"
	v, _ := newMAXValidator("secret", time.Hour)
	raw := signedInitData(t, "secret", values)
	for name, candidate := range map[string]string{
		"tampered":          strings.Replace(raw, "Ada", "Eve", 1),
		"duplicate":         raw + "&auth_date=1700000000",
		"missing hash":      strings.Replace(raw, "&hash=", "&nohash=", 1),
		"malformed hash":    strings.Replace(raw, "hash=", "hash=zz", 1),
		"malformed escape":  strings.Replace(raw, "Ada", "Ada%", 1),
		"malformed user":    signedInitData(t, "secret", map[string]string{"auth_date": "1700000000", "user": "{}"}),
		"invalid user JSON": signedInitData(t, "secret", map[string]string{"auth_date": "1700000000", "user": "{"}),
		"wrong user ID":     signedInitData(t, "secret", map[string]string{"auth_date": "1700000000", "user": `{"id":"42","first_name":"Ada"}`}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := v.validate(candidate, now); err != ErrUnauthenticated {
				t.Fatalf("got %v, want ErrUnauthenticated", err)
			}
		})
	}
}

func TestMAXValidatorFreshnessBoundariesAndPercentDecodedPlus(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	v, _ := newMAXValidator("secret", 10*time.Second)
	for name, age := range map[string]int64{"exact boundary": 10, "future": -1, "expired": 11} {
		t.Run(name, func(t *testing.T) {
			values := validValues(now)
			values["auth_date"] = strconv.FormatInt(now.Add(-time.Duration(age)*time.Second).Unix(), 10)
			raw := signedInitData(t, "secret", values)
			_, err := v.validate(raw, now)
			if age == 10 && err != nil {
				t.Fatalf("exact boundary rejected: %v", err)
			}
			if age != 10 && err != ErrUnauthenticated {
				t.Fatalf("got %v, want unauthenticated", err)
			}
		})
	}
	values := validValues(now)
	values["auth_date"] = "1700000000"
	values["start_param"] = "a%2Bb"
	// The raw percent escape is decoded before signing; '+' remains literal.
	if _, err := v.validate(signedInitData(t, "secret", values), now); err != nil {
		t.Fatalf("percent decoded value rejected: %v", err)
	}
}
