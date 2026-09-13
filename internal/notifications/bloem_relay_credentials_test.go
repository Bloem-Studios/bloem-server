package notifications

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"strings"
	"testing"
)

func TestDefaultPushRelayIsBloemsOwn(t *testing.T) {
	if DefaultPushRelayURL != "https://push.bloem-studios.com" {
		t.Fatalf("default push relay must be Bloem's own, got %q", DefaultPushRelayURL)
	}
	if strings.Contains(DefaultPushRelayURL, "siloserver.org") {
		t.Fatal("default push relay must never be Silo's relay")
	}

	// And the allow-list must actually refuse Silo's relay, so a stored setting
	// cannot reach it either.
	if _, err := NormalizePushRelayURL("https://push.siloserver.org", ""); err == nil {
		t.Fatal("Silo's relay must not be an accepted relay_url")
	}
}
