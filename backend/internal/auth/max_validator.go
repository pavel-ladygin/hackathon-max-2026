// Package auth owns MAX validation and application sessions. Other domains use contracts.Principal.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrTokenExpired    = errors.New("token expired")
)

// identity contains only verified claims needed during bootstrap; it is never serialized.
type identity struct {
	maxUserID   int64
	displayName string
	avatarURL   string
	locale      string
	startParam  string
}

type maxValidator struct {
	key    []byte
	maxAge time.Duration
}

func newMAXValidator(botToken string, maxAge time.Duration) (maxValidator, error) {
	if strings.TrimSpace(botToken) == "" || maxAge <= 0 {
		return maxValidator{}, errors.New("MAX_BOT_TOKEN and positive MAX_INIT_DATA_MAX_AGE are required")
	}
	// Official MAX algorithm: https://dev.max.ru/docs/webapps/validation
	// secret_key = HMAC-SHA256(key="WebAppData", message=BOT_TOKEN).
	mac := hmac.New(sha256.New, []byte("WebAppData"))
	mac.Write([]byte(botToken))
	return maxValidator{key: mac.Sum(nil), maxAge: maxAge}, nil
}

func (v maxValidator) validate(raw string, now time.Time) (identity, error) {
	invalid := func() (identity, error) { return identity{}, ErrUnauthenticated }
	if raw == "" || len(raw) > 16384 {
		return invalid()
	}
	params := make(map[string]string)
	keys := make([]string, 0)
	for _, pair := range strings.Split(raw, "&") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return invalid()
		}
		// MAX specifies literal keys and percent-decoded values (decodeURIComponent).
		// In particular '+' is a literal plus, not a form-encoded space.
		for _, c := range key {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
				return invalid()
			}
		}
		if _, duplicate := params[key]; duplicate {
			return invalid()
		}
		decoded, err := url.PathUnescape(value)
		if err != nil || !utf8.ValidString(decoded) {
			return invalid()
		}
		params[key] = decoded
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	signature, err := hex.DecodeString(params["hash"])
	if err != nil || len(signature) != sha256.Size {
		return invalid()
	}
	sort.Strings(keys)
	check := make([]string, 0, len(keys))
	for _, key := range keys {
		check = append(check, key+"="+params[key])
	}
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte(strings.Join(check, "\n")))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return invalid()
	}
	seconds, err := strconv.ParseInt(params["auth_date"], 10, 64)
	if err != nil || seconds <= 0 || seconds > now.Unix() {
		return invalid()
	}
	if now.Sub(time.Unix(seconds, 0)) > v.maxAge {
		return invalid()
	}
	var user struct {
		ID           int64  `json:"id"`
		FirstName    string `json:"first_name"`
		LastName     string `json:"last_name"`
		PhotoURL     string `json:"photo_url"`
		LanguageCode string `json:"language_code"`
	}
	if err := json.Unmarshal([]byte(params["user"]), &user); err != nil || user.ID <= 0 || strings.TrimSpace(user.FirstName) == "" {
		return invalid()
	}
	if user.PhotoURL != "" {
		u, err := url.Parse(user.PhotoURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return invalid()
		}
	}
	locale := strings.TrimSpace(user.LanguageCode)
	if locale == "" || locale == "ru" {
		locale = "ru-RU"
	}
	return identity{maxUserID: user.ID, displayName: strings.TrimSpace(user.FirstName + " " + user.LastName), avatarURL: user.PhotoURL, locale: locale, startParam: params["start_param"]}, nil
}
