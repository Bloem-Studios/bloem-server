package routeinventory

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"strings"
	"testing"
)

func TestOnlyReadOnlyMatchMethodMayEscape(t *testing.T) {
	if _, err := Analyze(fixtureConfig("match_method_value")); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(fixtureConfig("registration_method_value")); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("registration method escaped: %v", err)
	}
}
