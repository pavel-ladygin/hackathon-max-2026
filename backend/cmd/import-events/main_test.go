package main

import (
	"strings"
	"testing"
)

func TestRunRejectsUnsupportedArgumentsBeforeLoadingConfig(t *testing.T) {
	for name, args := range map[string][]string{
		"provider": {"--provider=other", "--city=moscow"},
		"city":     {"--provider=kudago", "--city=other"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(args); err == nil || !strings.Contains(err.Error(), "must be") {
				t.Fatalf("run(%v) error = %v", args, err)
			}
		})
	}
}
