// Package config loads the process configuration from its environment.
package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Config contains the configuration required to start a backend process.
type Config struct {
	AppEnv                     string
	HTTPAddr                   string
	DatabaseURL                string
	LogLevel                   slog.Level
	MAXBotToken                string
	MAXInitDataMaxAge          time.Duration
	TrustedProxyCIDRs          []net.IPNet
	InviteEncryptionKey        []byte
	InviteEncryptionKeyVersion int16
	InviteURLTemplate          string
	MAXDeepLinkTemplate        string
	TicketProviderAllowlist    []string
	KudaGoBaseURL              string
	KudaGoTimeout              time.Duration
	KudaGoLocation             string
	KudaGoPageSize             int
}

// Load reads and validates all required environment variables.
func Load() (Config, error) {
	cfg := Config{
		AppEnv:                     strings.TrimSpace(os.Getenv("APP_ENV")),
		HTTPAddr:                   strings.TrimSpace(os.Getenv("HTTP_ADDR")),
		DatabaseURL:                strings.TrimSpace(os.Getenv("DATABASE_URL")),
		MAXBotToken:                strings.TrimSpace(os.Getenv("MAX_BOT_TOKEN")),
		MAXInitDataMaxAge:          time.Hour,
		InviteEncryptionKeyVersion: 1,
		InviteURLTemplate:          strings.TrimSpace(os.Getenv("INVITE_URL_TEMPLATE")),
		MAXDeepLinkTemplate:        strings.TrimSpace(os.Getenv("MAX_DEEP_LINK_TEMPLATE")),
		KudaGoBaseURL:              "https://kudago.com/public-api/v1.4",
		KudaGoTimeout:              30 * time.Second,
		KudaGoLocation:             "msk",
		KudaGoPageSize:             100,
	}
	cfg.TicketProviderAllowlist = append([]string{"kudago.com", "*.kudago.com"}, splitList(os.Getenv("TICKET_PROVIDER_ALLOWLIST"))...)
	if value := strings.TrimSpace(os.Getenv("KUDAGO_BASE_URL")); value != "" {
		cfg.KudaGoBaseURL = value
	}
	if value := strings.TrimSpace(os.Getenv("KUDAGO_LOCATION")); value != "" {
		cfg.KudaGoLocation = value
	}
	if value := strings.TrimSpace(os.Getenv("KUDAGO_TIMEOUT")); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("KUDAGO_TIMEOUT is invalid")
		}
		cfg.KudaGoTimeout = timeout
	}
	if value := strings.TrimSpace(os.Getenv("KUDAGO_PAGE_SIZE")); value != "" {
		pageSize, err := strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return Config{}, fmt.Errorf("KUDAGO_PAGE_SIZE is invalid")
		}
		cfg.KudaGoPageSize = pageSize
	}
	if !validProviderBaseURL(cfg.KudaGoBaseURL) {
		return Config{}, fmt.Errorf("KUDAGO_BASE_URL is invalid")
	}
	// Migration and seed commands do not need invite credentials. The server
	// requires them when constructing its routes; malformed provided values
	// always fail without including secret values in errors.
	if encoded := strings.TrimSpace(os.Getenv("INVITE_ENCRYPTION_KEY")); encoded != "" {
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return Config{}, fmt.Errorf("INVITE_ENCRYPTION_KEY must be base64 encoding of 32 bytes")
		}
		cfg.InviteEncryptionKey = key
	}
	if value := strings.TrimSpace(os.Getenv("INVITE_ENCRYPTION_KEY_VERSION")); value != "" {
		version, err := strconv.ParseInt(value, 10, 16)
		if err != nil || version < 1 {
			return Config{}, fmt.Errorf("INVITE_ENCRYPTION_KEY_VERSION is invalid")
		}
		cfg.InviteEncryptionKeyVersion = int16(version)
	}
	trustedProxyCIDRs, err := parseCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return Config{}, err
	}
	cfg.TrustedProxyCIDRs = trustedProxyCIDRs
	logLevel := strings.TrimSpace(os.Getenv("LOG_LEVEL"))
	if value := strings.TrimSpace(os.Getenv("MAX_INIT_DATA_MAX_AGE")); value != "" {
		age, err := time.ParseDuration(value)
		if err != nil || age <= 0 {
			return Config{}, fmt.Errorf("MAX_INIT_DATA_MAX_AGE is invalid")
		}
		cfg.MAXInitDataMaxAge = age
	}
	if cfg.AppEnv == "" {
		return Config{}, fmt.Errorf("APP_ENV is required")
	}
	if cfg.HTTPAddr == "" {
		return Config{}, fmt.Errorf("HTTP_ADDR is required")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if logLevel == "" {
		return Config{}, fmt.Errorf("LOG_LEVEL is required")
	}
	_, portText, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR is invalid")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != portText {
		return Config{}, fmt.Errorf("HTTP_ADDR is invalid")
	}
	if _, err := pgx.ParseConfig(cfg.DatabaseURL); err != nil {
		return Config{}, fmt.Errorf("DATABASE_URL is invalid")
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(logLevel)); err != nil {
		return Config{}, fmt.Errorf("LOG_LEVEL is invalid")
	}
	return cfg, nil
}

func validProviderBaseURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func parseCIDRs(raw string) ([]net.IPNet, error) {
	var result []net.IPNet
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS is invalid")
		}
		result = append(result, *network)
	}
	return result, nil
}

func splitList(raw string) []string {
	var result []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}
