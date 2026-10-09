package main

import (
	"strings"
	"testing"
)

func TestFinalizeNeedsConfirmationToEndSiloSwitching(t *testing.T) {
	for _, tc := range []struct {
		phase     string
		confirmed bool
		refused   bool
	}{
		{phase: "compatibility"},
		{phase: "finalized"},
		{phase: "mirrored", refused: true},
		{phase: "mirrored", confirmed: true},
	} {
		err := checkFinalizeEndsSiloSwitching(tc.phase, tc.confirmed)
		if tc.refused != (err != nil) {
			t.Fatalf("phase=%s confirmed=%t: err = %v, refused want %t", tc.phase, tc.confirmed, err, tc.refused)
		}
		if err != nil && !strings.Contains(err.Error(), "--end-silo-switching") {
			t.Fatalf("refusal %q does not name the confirmation flag", err)
		}
	}
}
