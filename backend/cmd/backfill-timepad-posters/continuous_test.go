package main

import "testing"

func TestContinuousModeRequiresApplyAndNoPilotFilter(t *testing.T) {
	tests := []struct {
		name       string
		apply      bool
		externalID string
		wantErr    bool
	}{
		{name: "continuous apply", apply: true},
		{name: "continuous dry run", wantErr: true},
		{name: "continuous pilot filter", apply: true, externalID: "123", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateMode(50, test.externalID, test.apply, true, 0)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateMode() error=%v, wantErr=%t", err, test.wantErr)
			}
		})
	}
}

func TestManualDryRunRemainsValid(t *testing.T) {
	if err := validateMode(20, "123,456", false, false, 0); err != nil {
		t.Fatalf("manual dry-run rejected: %v", err)
	}
}
