package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestInviteConfigurationIsOptionalForUtilityCommandsButTypedWhenProvided(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("INVITE_ENCRYPTION_KEY", "")
	t.Setenv("INVITE_ENCRYPTION_KEY_VERSION", "")
	t.Setenv("INVITE_URL_TEMPLATE", "  https://app.test/invite/{token}  ")
	t.Setenv("MAX_DEEP_LINK_TEMPLATE", "  https://max.test/bot?startapp={token}  ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() without invite key = %v; migrations and seed must remain configurable", err)
	}
	if len(cfg.InviteEncryptionKey) != 0 || cfg.InviteEncryptionKeyVersion != 1 {
		t.Fatalf("optional invite key/version = %d/%d; want empty/1", len(cfg.InviteEncryptionKey), cfg.InviteEncryptionKeyVersion)
	}
	if cfg.InviteURLTemplate != "https://app.test/invite/{token}" || cfg.MAXDeepLinkTemplate != "https://max.test/bot?startapp={token}" {
		t.Fatalf("templates were not trimmed: %+v", cfg)
	}

	key := bytes.Repeat([]byte{0x3a}, 32)
	t.Setenv("INVITE_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("INVITE_ENCRYPTION_KEY_VERSION", "17")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cfg.InviteEncryptionKey, key) || cfg.InviteEncryptionKeyVersion != 17 {
		t.Fatalf("decoded invite configuration = %d bytes/version %d", len(cfg.InviteEncryptionKey), cfg.InviteEncryptionKeyVersion)
	}
}

func TestInviteConfigurationRejectsBadValuesWithoutLeakingKey(t *testing.T) {
	secret := "unacceptable-invite-key-material"
	for name, value := range map[string]string{
		"invalid base64":       secret,
		"wrong decoded length": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31)),
	} {
		t.Run(name, func(t *testing.T) {
			setValidConfigEnv(t)
			t.Setenv("INVITE_ENCRYPTION_KEY", value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "INVITE_ENCRYPTION_KEY") || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), value) {
				t.Fatalf("Load() error = %v; want safe invite key error", err)
			}
		})
	}
	for _, value := range []string{"0", "-1", "32768", "not-a-number"} {
		t.Run("invalid version "+value, func(t *testing.T) {
			setValidConfigEnv(t)
			t.Setenv("INVITE_ENCRYPTION_KEY_VERSION", value)
			_, err := Load()
			if err == nil || err.Error() != "INVITE_ENCRYPTION_KEY_VERSION is invalid" {
				t.Fatalf("Load() error = %v; want safe invite version error", err)
			}
		})
	}
}
