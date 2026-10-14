package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// bloemCompatServerIDKey is the server_settings row that holds the server ID
// upstream Silo answers on GET /api/v1/health. Silo seeds it at startup from
// its Jellyfin-compatibility configuration, which Bloem removed. The row
// stays in the database for Silo switching, and Bloem only reads it, so a
// client that stored this ID before the removal keeps recognizing the
// server.
const bloemCompatServerIDKey = "jellyfin_compat.server_id"

const (
	// bloemHealthIdentityTimeout bounds the identity reads on one health
	// request. Health checks nothing, so a slow database may delay it this
	// long at most and then answers without identity.
	bloemHealthIdentityTimeout = 500 * time.Millisecond
	// bloemHealthNameTTL is how long a resolved server name is reused, so a
	// branding rename shows up without a restart while liveness probes stay
	// off the database.
	bloemHealthNameTTL = 30 * time.Second
	// bloemHealthRetryWait spaces out reads after one fails.
	bloemHealthRetryWait = time.Second
)

// HealthIdentitySettings reads one server setting; a missing row reads as "".
// *catalog.ServerSettingsRepo satisfies it.
type HealthIdentitySettings interface {
	Get(ctx context.Context, key string) (string, error)
}

// HealthServerNamer resolves the operator-facing server name, reporting a
// failed read instead of a default. *branding.Service satisfies it.
type HealthServerNamer interface {
	ServerName(ctx context.Context) (string, error)
}

// HealthServerIdentity is the deployment identity, minted on first use.
// *serveridentity.Service satisfies it.
type HealthServerIdentity interface {
	ServerID(ctx context.Context) (string, error)
}

// BloemHealthHandler serves GET /api/v1/health with the identity fields
// upstream Silo's health returns, read from the database instead of the
// Jellyfin-compatibility configuration:
//
//   - server_id is the preserved jellyfin_compat.server_id row, the value
//     Silo answered before the removal. A database that never had one (a
//     fresh Bloem install) answers the deployment identity instead, the
//     server_id of GET /api/v2/system/identity.
//   - server_name is the branding name, the same name the web UI and
//     GET /api/bloem/v1/server/identity show.
//
// The body, status and headers are upstream's HealthHandler. Health still
// checks nothing: identity is cached, reads are bounded, and a failed read
// omits the field, as upstream does when the identity is unconfigured.
type BloemHealthHandler struct {
	settings HealthIdentitySettings
	names    HealthServerNamer
	identity HealthServerIdentity
	now      func() time.Time

	mu          sync.Mutex
	serverID    string
	serverName  string
	nextRefresh time.Time
	refreshing  bool
}

// NewBloemHealthHandler builds the health handler over the settings store,
// the branding name and the deployment identity. All three are required.
func NewBloemHealthHandler(settings HealthIdentitySettings, names HealthServerNamer, identity HealthServerIdentity) *BloemHealthHandler {
	return &BloemHealthHandler{settings: settings, names: names, identity: identity, now: time.Now}
}

// ServeHTTP answers like upstream's HealthHandler, with the current identity.
func (h *BloemHealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, id := h.current(r.Context())
	NewHealthHandler(name, id).ServeHTTP(w, r)
}

// current returns the cached identity, refreshing it when it is due. One
// request refreshes at a time; requests that arrive meanwhile answer with the
// cached values rather than wait. The server ID never changes once resolved.
func (h *BloemHealthHandler) current(ctx context.Context) (name, id string) {
	h.mu.Lock()
	name, id = h.serverName, h.serverID
	due := !h.refreshing && !h.now().Before(h.nextRefresh)
	if due {
		h.refreshing = true
	}
	h.mu.Unlock()
	if !due {
		return name, id
	}

	ctx, cancel := context.WithTimeout(ctx, bloemHealthIdentityTimeout)
	defer cancel()
	freshName, nameErr := h.names.ServerName(ctx)
	freshID := id
	if freshID == "" {
		freshID = h.resolveServerID(ctx)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.refreshing = false
	if nameErr == nil {
		h.serverName = strings.TrimSpace(freshName)
	}
	h.serverID = freshID
	if nameErr == nil && freshID != "" {
		h.nextRefresh = h.now().Add(bloemHealthNameTTL)
	} else {
		h.nextRefresh = h.now().Add(bloemHealthRetryWait)
	}
	return h.serverName, h.serverID
}

// resolveServerID prefers the preserved compatibility ID and falls back to
// the deployment identity only when that row is absent or empty. A failed
// read answers "" so the next refresh tries again; it never falls back,
// because an outage must not be mistaken for a fresh install.
func (h *BloemHealthHandler) resolveServerID(ctx context.Context) string {
	preserved, err := h.settings.Get(ctx, bloemCompatServerIDKey)
	if err != nil {
		return ""
	}
	if id := strings.TrimSpace(preserved); id != "" {
		return id
	}
	minted, err := h.identity.ServerID(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(minted)
}
