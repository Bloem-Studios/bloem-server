package compatgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"
)

func newTestDiscovery(t *testing.T, states StateProvider) *Discovery {
	t.Helper()
	return NewDiscovery(DiscoveryConfig{
		PublicAddress: "https://vondel.example",
		ServerID:      "compat-server-id",
		ServerName:    "Vondel",
		States:        states,
	})
}

func availableJellyfin() *fakeStates {
	states := &fakeStates{}
	endpoint, _ := url.Parse("http://vondel-jellyfin:8096")
	states.set(KindJellyfin, availableStatus(endpoint))
	return states
}

func TestDiscoveryAnswersOnlyTheJellyfinProbe(t *testing.T) {
	discovery := newTestDiscovery(t, availableJellyfin())
	for _, probe := range []string{"who is JellyfinServer?", "WHO IS JELLYFINSERVER?", " who is jellyfinserver? "} {
		response, ok := discovery.Respond(context.Background(), []byte(probe))
		if !ok || len(response) == 0 {
			t.Fatalf("probe %q must be answered while jellyfin is available", probe)
		}
	}
	for _, junk := range []string{"", "who is EmbyServer?", "GET / HTTP/1.1", "who is jellyfinserver"} {
		if _, ok := discovery.Respond(context.Background(), []byte(junk)); ok {
			t.Fatalf("payload %q must not be answered", junk)
		}
	}
}

// The relay advertises the canonical address and stable ID and nothing else:
// no users, profiles, organizations, internal addresses, or topology.
func TestDiscoveryDisclosesOnlyTheCanonicalAddress(t *testing.T) {
	discovery := newTestDiscovery(t, availableJellyfin())
	response, ok := discovery.Respond(context.Background(), []byte("who is jellyfinserver?"))
	if !ok {
		t.Fatal("expected a discovery response")
	}
	var payload map[string]any
	if err := json.Unmarshal(response, &payload); err != nil {
		t.Fatalf("response must be JSON: %v", err)
	}
	for _, required := range []string{"Address", "Id", "Name"} {
		if _, ok := payload[required]; !ok {
			t.Fatalf("response missing %q: %s", required, response)
		}
	}
	if len(payload) != 3 {
		t.Fatalf("response must carry exactly Address, Id, Name; got %s", response)
	}
	if payload["Address"] != "https://vondel.example" {
		t.Fatalf("advertised address %q must be the canonical public one", payload["Address"])
	}
	if payload["Id"] != "compat-server-id" {
		t.Fatalf("advertised id %q must be the stable compatibility server id", payload["Id"])
	}
}

// The relay responds only while an enrolled Jellyfin companion is enabled,
// healthy, and compatible; every other state stays silent.
func TestDiscoveryStaysSilentWhenJellyfinIsUnavailable(t *testing.T) {
	endpoint, _ := url.Parse("http://vondel-jellyfin:8096")
	cases := map[string]Status{
		"absent":       {},
		"disabled":     {Known: true, Enrolled: true, Healthy: true, APICompatible: true, Endpoint: endpoint},
		"revoked":      {Known: true, Enrolled: true, Enabled: true, Revoked: true, Healthy: true, APICompatible: true, Endpoint: endpoint},
		"unhealthy":    {Known: true, Enrolled: true, Enabled: true, APICompatible: true, Endpoint: endpoint},
		"incompatible": {Known: true, Enrolled: true, Enabled: true, Healthy: true, Endpoint: endpoint},
	}
	for name, status := range cases {
		states := &fakeStates{}
		states.set(KindJellyfin, status)
		discovery := newTestDiscovery(t, states)
		if _, ok := discovery.Respond(context.Background(), []byte("who is jellyfinserver?")); ok {
			t.Fatalf("%s: discovery must stay silent", name)
		}
	}

	discovery := newTestDiscovery(t, &fakeStates{err: errors.New("store down")})
	if _, ok := discovery.Respond(context.Background(), []byte("who is jellyfinserver?")); ok {
		t.Fatal("a state failure must stay silent, not advertise")
	}
}

// Responses are rate-limited so the relay cannot be used for amplification.
func TestDiscoveryRateLimits(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	discovery := NewDiscovery(DiscoveryConfig{
		PublicAddress: "https://vondel.example",
		ServerID:      "compat-server-id",
		ServerName:    "Vondel",
		States:        availableJellyfin(),
		RateLimit:     2,
		RateWindow:    time.Minute,
		Now:           func() time.Time { return now },
	})

	probe := []byte("who is jellyfinserver?")
	for i := 0; i < 2; i++ {
		if _, ok := discovery.Respond(context.Background(), probe); !ok {
			t.Fatalf("probe %d within the budget must be answered", i)
		}
	}
	if _, ok := discovery.Respond(context.Background(), probe); ok {
		t.Fatal("probes beyond the budget must be dropped")
	}
	now = now.Add(61 * time.Second)
	if _, ok := discovery.Respond(context.Background(), probe); !ok {
		t.Fatal("the budget must refill after the window")
	}
}
