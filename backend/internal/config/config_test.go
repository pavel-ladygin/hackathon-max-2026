package config

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func setValidConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("DATABASE_URL", "postgres://test_user:test_password@127.0.0.1:5432/test_db")
	t.Setenv("LOG_LEVEL", "INFO")
	t.Setenv("MAX_INIT_DATA_MAX_AGE", "")
	t.Setenv("MAX_BOT_TOKEN", "")
}

func TestMAXConfig(t *testing.T) {
	setValidConfigEnv(t)
	cfg, err := Load()
	if err != nil || cfg.MAXInitDataMaxAge != time.Hour {
		t.Fatal("MAX max age default must be 1h")
	}
	// Migration commands use Config too and do not need a bot credential.
	if cfg.MAXBotToken != "" {
		t.Fatal("unexpected bot token")
	}
	t.Setenv("MAX_INIT_DATA_MAX_AGE", "10m")
	t.Setenv("MAX_BOT_TOKEN", "test-only-bot-token")
	cfg, err = Load()
	if err != nil || cfg.MAXInitDataMaxAge != 10*time.Minute || cfg.MAXBotToken != "test-only-bot-token" {
		t.Fatal("MAX configuration override failed")
	}
	for _, value := range []string{"0", "-1m", "invalid-secret-value"} {
		t.Setenv("MAX_INIT_DATA_MAX_AGE", value)
		_, err := Load()
		if err == nil || err.Error() != "MAX_INIT_DATA_MAX_AGE is invalid" {
			t.Fatal("invalid MAX max age accepted or error leaked value")
		}
	}
}

func TestLoadValidatesRequiredAndTypedValues(t *testing.T) {
	for _, name := range []string{"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "LOG_LEVEL"} {
		t.Run("missing "+name, func(t *testing.T) {
			setValidConfigEnv(t)
			t.Setenv(name, "")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name+" is required") {
				t.Fatalf("Load() error = %v, want required %s error", err, name)
			}
		})
	}

	for _, address := range []string{"localhost", ":", ":0", ":notaport", ":65536", ":-1"} {
		t.Run("invalid address "+address, func(t *testing.T) {
			setValidConfigEnv(t)
			t.Setenv("HTTP_ADDR", address)
			_, err := Load()
			if err == nil || err.Error() != "HTTP_ADDR is invalid" {
				t.Fatalf("Load() error = %v, want invalid address", err)
			}
		})
	}
	t.Run("invalid URL", func(t *testing.T) {
		setValidConfigEnv(t)
		t.Setenv("DATABASE_URL", "postgres://%zz")
		_, err := Load()
		if err == nil || err.Error() != "DATABASE_URL is invalid" {
			t.Fatalf("Load() error = %v, want invalid URL", err)
		}
	})
	t.Run("invalid log level", func(t *testing.T) {
		setValidConfigEnv(t)
		t.Setenv("LOG_LEVEL", "verbose")
		_, err := Load()
		if err == nil || err.Error() != "LOG_LEVEL is invalid" {
			t.Fatalf("Load() error = %v, want invalid log level", err)
		}
	})
}

func TestLoadDoesNotExposeDatabaseSecret(t *testing.T) {
	setValidConfigEnv(t)
	secret := "super-secret-password"
	t.Setenv("DATABASE_URL", "postgres://user:"+secret+"@%zz")
	_, err := Load()
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Load() error = %v, must be non-nil and omit database secret", err)
	}
	if _, parseErr := pgx.ParseConfig("postgres://user:" + secret + "@%zz"); parseErr == nil {
		t.Fatal("test URL unexpectedly parsed")
	}
}
