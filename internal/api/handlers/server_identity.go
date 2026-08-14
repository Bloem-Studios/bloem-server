package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/branding"
)

// SettingServerInstanceID is the server_settings key holding this deployment's
// stable identity. It is a PLAIN row on purpose: it is not a secret, every
// client that reaches the server is told the value, and encrypted rows are
// GCM-bound to their key name (see catalog.SensitiveSettingKeys), which would
// make the identifier unreadable after any future key rename.
//
// The row is never seeded by a migration. It is minted on first read through
// SetIfAbsent, so a fresh install and an upgraded install follow the identical
// path, and a restored backup keeps the identifier it already had — correctly,
// because it is the same server.
const SettingServerInstanceID = "server.instance_id"

// serverAPIMajorVersions are the API major versions this build serves. Clients
// use it to decide whether a discovered server is worth connecting to at all;
// everything finer-grained is feature-detected through GET /api/v1/capabilities
// rather than inferred from a version.
var serverAPIMajorVersions = []int{1}

// ServerIdentitySettings is the server_settings surface the identity endpoint
// needs. catalog.SettingsStore (and the encrypting decorator around it)
// satisfies it.
type ServerIdentitySettings interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// conditionalSettingsWriter is the optional insert-if-absent surface used to
// mint the instance identifier without racing a concurrent node. Both
// catalog.ServerSettingsRepo and catalog.EncryptedSettingsRepo satisfy it; the
// fallback for a store that does not is a plain Set.
type conditionalSettingsWriter interface {
	SetIfAbsent(ctx context.Context, key, value string) (bool, error)
}

// SetupStateReporter reports whether the deployment still needs its first
// account. *auth.Service satisfies it.
type SetupStateReporter interface {
	NeedsSetup(ctx context.Context) (bool, error)
}

// ServerIdentityHandler serves GET /api/v1/server/identity: the public,
// unauthenticated answer to "which server is this, and can I log into it yet".
//
// Relationship to GET /api/v1/health: none. Health keeps exactly the fields it
// has and exactly the source it has — server_name and server_id there come from
// the Jellyfin-compatibility configuration and stay omitted when that
// configuration is absent. Nothing reads SettingServerInstanceID into health,
// because clients (and Jellyfin-protocol clients especially) already depend on
// the values health returns today, and the v1 rules forbid repurposing them.
// Scope keying uses this endpoint instead, which is why it exists.
type ServerIdentityHandler struct {
	settings ServerIdentitySettings
	branding *branding.Service
	setup    SetupStateReporter

	// mu guards serverID, and is held across the first resolution so
	// concurrent first requests mint at most one candidate identifier between
	// them. Every later request is a cached read: this endpoint is
	// unauthenticated, and a database round trip per call would be a free
	// amplification vector.
	mu       sync.Mutex
	serverID string
}

// NewServerIdentityHandler constructs the identity handler. settings and setup
// are required for a truthful answer and are nil only where the database is
// absent (test and fixture wiring); branding is optional and falls back to the
// default server name.
func NewServerIdentityHandler(
	settings ServerIdentitySettings,
	brandingSvc *branding.Service,
	setup SetupStateReporter,
) *ServerIdentityHandler {
	return &ServerIdentityHandler{settings: settings, branding: brandingSvc, setup: setup}
}

// serverIdentityResponse is the body of GET /api/v1/server/identity.
//
// status is carried so the body also satisfies the client contract's
// ServerIdentity schema, which requires it; the rest is what scope keying
// needs. Fields are additive-only from here per the v1 rules.
type serverIdentityResponse struct {
	Status        string `json:"status"`
	ServerID      string `json:"server_id"`
	ServerName    string `json:"server_name"`
	APIVersions   []int  `json:"api_versions"`
	SetupComplete bool   `json:"setup_complete"`
}

// HandleGetServerIdentity answers the public identity probe.
//
// It reports 503 rather than inventing a value when the identifier cannot be
// resolved. A per-process random identifier would be stable within one run and
// change on restart, which silently re-keys every client's stored state for
// that server — strictly worse than a client falling back to its origin string.
func (h *ServerIdentityHandler) HandleGetServerIdentity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.settings == nil || h.setup == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Server identity is not available")
		return
	}

	serverID, err := h.ResolveServerID(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to resolve server instance id", "component", "api", "error", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Server identity is not available")
		return
	}

	needsSetup, err := h.setup.NeedsSetup(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read initial setup state", "component", "api", "error", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Server identity is not available")
		return
	}

	// no-store: the identifier never changes, but setup_complete flips exactly
	// once and a cached "false" would leave a client offering first-run setup
	// on a configured server.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, serverIdentityResponse{
		Status:        "ok",
		ServerID:      serverID,
		ServerName:    h.serverName(ctx),
		APIVersions:   append([]int(nil), serverAPIMajorVersions...),
		SetupComplete: !needsSetup,
	})
}

// ResolveServerID returns this deployment's stable identifier, minting and
// persisting one on first use. It is exported because the identifier is the
// same value the local-network advertisement publishes, and the two must never
// disagree.
//
// Minting is single-writer across concurrent nodes: SetIfAbsent only lands
// while the row is absent or empty, and the value is then read back so a node
// that lost the race adopts the winner instead of serving an identifier that is
// not in the database.
func (h *ServerIdentityHandler) ResolveServerID(ctx context.Context) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.serverID != "" {
		return h.serverID, nil
	}

	existing, err := h.settings.Get(ctx, SettingServerInstanceID)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", SettingServerInstanceID, err)
	}
	if id := strings.TrimSpace(existing); id != "" {
		h.serverID = id
		return id, nil
	}

	minted := uuid.NewString()
	writer, ok := h.settings.(conditionalSettingsWriter)
	if !ok {
		if err := h.settings.Set(ctx, SettingServerInstanceID, minted); err != nil {
			return "", fmt.Errorf("write %s: %w", SettingServerInstanceID, err)
		}
		h.serverID = minted
		return minted, nil
	}

	if _, err := writer.SetIfAbsent(ctx, SettingServerInstanceID, minted); err != nil {
		return "", fmt.Errorf("write %s: %w", SettingServerInstanceID, err)
	}
	winner, err := h.settings.Get(ctx, SettingServerInstanceID)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", SettingServerInstanceID, err)
	}
	if id := strings.TrimSpace(winner); id != "" {
		h.serverID = id
		return id, nil
	}
	// The row read back empty despite a successful write — a store that does
	// not persist. Serve the minted value rather than failing, but do not cache
	// it: the next request retries the write.
	return minted, nil
}

// serverName returns the operator-facing name, which is the branding name the
// web UI and login screen already show. Branding falls back to its own default,
// so this is never empty.
func (h *ServerIdentityHandler) serverName(ctx context.Context) string {
	if h.branding == nil {
		return branding.DefaultServerName
	}
	return h.branding.Load(ctx).ServerName
}
