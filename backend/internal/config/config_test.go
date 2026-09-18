package config

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func setValidConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("DATABASE_URL", "postgres://test_user:test_password@127.0.0.1:5432/test_db")
	t.Setenv("LOG_LEVEL", "INFO")
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
