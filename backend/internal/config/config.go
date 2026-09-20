// Package config loads the process configuration from its environment.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Config contains the configuration required to start a backend process.
type Config struct {
	AppEnv            string
	HTTPAddr          string
	DatabaseURL       string
	LogLevel          slog.Level
	MAXBotToken       string
	MAXInitDataMaxAge time.Duration
	TrustedProxyCIDRs []net.IPNet
}

// Load reads and validates all required environment variables.
func Load() (Config, error) {
	cfg := Config{
		AppEnv:            strings.TrimSpace(os.Getenv("APP_ENV")),
		HTTPAddr:          strings.TrimSpace(os.Getenv("HTTP_ADDR")),
		DatabaseURL:       strings.TrimSpace(os.Getenv("DATABASE_URL")),
		MAXBotToken:       strings.TrimSpace(os.Getenv("MAX_BOT_TOKEN")),
		MAXInitDataMaxAge: time.Hour,
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
