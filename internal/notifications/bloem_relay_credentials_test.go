package notifications

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"testing"
)

// Silo's push path keeps Silo's relay, so Silo's apps get push from a Bloem
// server; Bloem's relay is a separate origin that path never accepts.
func TestPushRelayOriginsStaySeparate(t *testing.T) {
	if DefaultPushRelayURL != "https://push.siloserver.org" {
		t.Fatalf("Silo's push path must use Silo's relay, got %q", DefaultPushRelayURL)
	}
	if BloemPushRelayURL != "https://push.bloem-studios.com" {
		t.Fatalf("Bloem's relay origin is %q", BloemPushRelayURL)
	}
	if _, err := NormalizePushRelayURL(BloemPushRelayURL, ""); err == nil {
		t.Fatal("Silo's push path must not accept Bloem's relay as a relay_url")
	}
	for _, key := range relayCredentialKeys {
		if bloemRelayKey(key) == key || bloemRelayKey(key) != "bloem."+key {
			t.Fatalf("Bloem's relay key for %s is %s", key, bloemRelayKey(key))
		}
	}
}
