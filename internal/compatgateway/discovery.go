package compatgateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// DiscoveryConfig configures the optional Jellyfin LAN discovery relay. The
// relay exists because companions have no public ports: Bloem answers the
// Jellyfin discovery probe on their behalf, advertising only the canonical
// public address and the stable compatibility server ID.
type DiscoveryConfig struct {
	// PublicAddress is the canonical Bloem HTTPS address to advertise.
	PublicAddress string
	// ServerID is the stable Jellyfin-compatibility server identifier.
	ServerID string
	// ServerName is the display name to advertise.
	ServerName string
	// States gates the relay: it answers only while an enrolled Jellyfin
	// application is enabled, healthy, and API-compatible.
	States StateProvider
	// RateLimit is the maximum number of responses per RateWindow. Defaults
	// to 30 to avoid amplification abuse.
	RateLimit int
	// RateWindow is the rate-limit window. Defaults to one minute.
	RateWindow time.Duration
	// Now is the clock, for tests.
	Now func() time.Time
}

const (
	discoveryProbe        = "who is jellyfinserver?"
	defaultDiscoveryLimit = 30
)

// Discovery answers the Jellyfin LAN discovery probe. It discloses no users,
// profiles, organizations, internal addresses, or topology.
type Discovery struct {
	publicAddress string
	serverID      string
	serverName    string
	states        StateProvider
	rateLimit     int
	rateWindow    time.Duration
	now           func() time.Time

	mu          sync.Mutex
	windowStart time.Time
	windowCount int
}

// NewDiscovery builds the discovery relay.
func NewDiscovery(cfg DiscoveryConfig) *Discovery {
	discovery := &Discovery{
		publicAddress: cfg.PublicAddress,
		serverID:      cfg.ServerID,
		serverName:    cfg.ServerName,
		states:        cfg.States,
		rateLimit:     cfg.RateLimit,
		rateWindow:    cfg.RateWindow,
		now:           cfg.Now,
	}
	if discovery.rateLimit <= 0 {
		discovery.rateLimit = defaultDiscoveryLimit
	}
	if discovery.rateWindow <= 0 {
		discovery.rateWindow = time.Minute
	}
	if discovery.now == nil {
		discovery.now = time.Now
	}
	return discovery
}

// Respond answers a single discovery datagram. It returns (payload, true)
// only for the exact Jellyfin probe, only while the Jellyfin application is
// routable, and only within the rate budget; every other case stays silent.
func (d *Discovery) Respond(ctx context.Context, payload []byte) ([]byte, bool) {
	if !strings.EqualFold(strings.TrimSpace(string(payload)), discoveryProbe) {
		return nil, false
	}
	if d.states == nil {
		return nil, false
	}
	status, err := d.states.ApplicationStatus(ctx, KindJellyfin)
	if err != nil || !status.Routable() {
		return nil, false
	}
	if !d.allow() {
		return nil, false
	}
	response, err := json.Marshal(struct {
		Address string `json:"Address"`
		ID      string `json:"Id"`
		Name    string `json:"Name"`
	}{d.publicAddress, d.serverID, d.serverName})
	if err != nil {
		return nil, false
	}
	return response, true
}

// allow consumes one unit of the fixed-window rate budget.
func (d *Discovery) allow() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if d.windowStart.IsZero() || now.Sub(d.windowStart) >= d.rateWindow {
		d.windowStart = now
		d.windowCount = 0
	}
	if d.windowCount >= d.rateLimit {
		return false
	}
	d.windowCount++
	return true
}
