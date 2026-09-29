package config

import (
	"net"
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
	t.Setenv("INVITE_ENCRYPTION_KEY", "")
	t.Setenv("INVITE_ENCRYPTION_KEY_VERSION", "")
	t.Setenv("INVITE_URL_TEMPLATE", "")
	t.Setenv("MAX_DEEP_LINK_TEMPLATE", "")
	t.Setenv("TICKET_PROVIDER_ALLOWLIST", "")
	t.Setenv("KUDAGO_BASE_URL", "")
	t.Setenv("KUDAGO_TIMEOUT", "")
	t.Setenv("KUDAGO_LOCATION", "")
	t.Setenv("KUDAGO_PAGE_SIZE", "")
	t.Setenv("EVENT_SYNC_INTERVAL", "")
	t.Setenv("EVENT_SYNC_RUN_ON_START", "")
	t.Setenv("TIMEPAD_BASE_URL", "")
	t.Setenv("TIMEPAD_TOKEN", "")
	t.Setenv("TIMEPAD_TIMEOUT", "")
	t.Setenv("TIMEPAD_PAGE_SIZE", "")
	t.Setenv("TIMEPAD_MAX_REQUESTS_PER_MINUTE", "")
}

func TestTimepadSyncDefaultsAndOverrides(t *testing.T) {
	setValidConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EventSyncInterval != 2*time.Hour || cfg.EventSyncRunOnStart || cfg.TimepadMaxRequestsPerMinute != 20 {
		t.Fatalf("unexpected Timepad sync defaults: %+v", cfg)
	}
	t.Setenv("EVENT_SYNC_INTERVAL", "15m")
	t.Setenv("EVENT_SYNC_RUN_ON_START", "true")
	t.Setenv("TIMEPAD_MAX_REQUESTS_PER_MINUTE", "7")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EventSyncInterval != 15*time.Minute || !cfg.EventSyncRunOnStart || cfg.TimepadMaxRequestsPerMinute != 7 {
		t.Fatalf("unexpected Timepad sync overrides: %+v", cfg)
	}
}

func TestTimepadSyncRejectsInvalidValues(t *testing.T) {
	for name, value := range map[string]string{"EVENT_SYNC_RUN_ON_START": "maybe", "TIMEPAD_MAX_REQUESTS_PER_MINUTE": "0"} {
		t.Run(name, func(t *testing.T) {
			setValidConfigEnv(t)
			t.Setenv(name, value)
			_, err := Load()
			if err == nil || err.Error() != name+" is invalid" {
				t.Fatalf("Load() error=%v", err)
			}
		})
	}
}

func TestKudaGoConfigDefaultsAndOverrides(t *testing.T) {
	setValidConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KudaGoBaseURL != "https://kudago.com/public-api/v1.4" || cfg.KudaGoTimeout != 30*time.Second || cfg.KudaGoLocation != "msk" || cfg.KudaGoPageSize != 100 {
		t.Fatalf("unexpected KudaGo defaults: %+v", cfg)
	}

	t.Setenv("KUDAGO_BASE_URL", "http://127.0.0.1:9090/api")
	t.Setenv("KUDAGO_TIMEOUT", "3s")
	t.Setenv("KUDAGO_LOCATION", "test-city")
	t.Setenv("KUDAGO_PAGE_SIZE", "25")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KudaGoBaseURL != "http://127.0.0.1:9090/api" || cfg.KudaGoTimeout != 3*time.Second || cfg.KudaGoLocation != "test-city" || cfg.KudaGoPageSize != 25 {
		t.Fatalf("unexpected KudaGo overrides: %+v", cfg)
	}
}

func TestKudaGoConfigRejectsInvalidValues(t *testing.T) {
	for name, value := range map[string]string{
		"KUDAGO_BASE_URL":  "relative/path",
		"KUDAGO_TIMEOUT":   "0s",
		"KUDAGO_PAGE_SIZE": "101",
	} {
		t.Run(name, func(t *testing.T) {
			setValidConfigEnv(t)
			t.Setenv(name, value)
			_, err := Load()
			if err == nil || err.Error() != name+" is invalid" {
				t.Fatalf("Load() error=%v", err)
			}
		})
	}
}

func TestLoadParsesTicketProviderAllowlist(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("TICKET_PROVIDER_ALLOWLIST", " tickets.example , *.partner.example ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(cfg.TicketProviderAllowlist, ","), "kudago.com,*.kudago.com,timepad.ru,*.timepad.ru,tickets.example,*.partner.example"; got != want {
		t.Fatalf("ticket provider allowlist = %q, want %q", got, want)
	}
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

func TestLoadParsesTrustedProxyCIDRs(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("TRUSTED_PROXY_CIDRS", "127.0.0.0/8, 2001:db8::/32")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 || !cfg.TrustedProxyCIDRs[0].Contains(net.ParseIP("127.0.0.1")) || !cfg.TrustedProxyCIDRs[1].Contains(net.ParseIP("2001:db8::1")) {
		t.Fatalf("unexpected trusted proxy CIDRs: %#v", cfg.TrustedProxyCIDRs)
	}
}

func TestLoadRejectsInvalidTrustedProxyCIDRs(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("TRUSTED_PROXY_CIDRS", "127.0.0.1")
	if _, err := Load(); err == nil || err.Error() != "TRUSTED_PROXY_CIDRS is invalid" {
		t.Fatalf("Load() error = %v, want invalid CIDR error", err)
	}
}
