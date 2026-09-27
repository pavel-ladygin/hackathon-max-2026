package store

import "testing"

func TestApplicationPoolConfigDisablesJITAndPreservesOtherRuntimeParams(t *testing.T) {
	config, err := applicationPoolConfig("postgres://user:pass@localhost/db?sslmode=disable&application_name=max-together&jit=on")
	if err != nil {
		t.Fatalf("applicationPoolConfig() error = %v", err)
	}
	if got := config.ConnConfig.RuntimeParams["jit"]; got != "off" {
		t.Errorf("runtime jit = %q, want off", got)
	}
	if got := config.ConnConfig.RuntimeParams["application_name"]; got != "max-together" {
		t.Errorf("runtime application_name = %q, want max-together", got)
	}
}
