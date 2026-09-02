package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/branding"
	"github.com/Silo-Server/silo-server/internal/serverid"
)

// serverAPIMajorVersions are the Silo-compatible API major versions this build
// serves. It says nothing about the native surface: upstream Silo will serve
// its own major 2, so a number cannot tell a client whether the server it
// reached speaks this project's API. That is what nativeAPISurfaces is for.
var serverAPIMajorVersions = []int{1}

// nativeAPISurfaces are the native surface versions mounted under /api/bloem/.
// Clients use the list to decide whether a discovered server speaks this API at
// all; everything finer-grained is feature-detected through
// GET /api/bloem/v1/capabilities, never inferred from a version.
var nativeAPISurfaces = []string{"v1"}

// SetupStateReporter reports whether the deployment still needs its first
// account. *auth.Service satisfies it.
type SetupStateReporter interface {
	NeedsSetup(ctx context.Context) (bool, error)
}

// ServerIdentityHandler serves GET /api/bloem/v1/server/identity: the public,
// unauthenticated answer to "which server is this, and can I log into it yet".
//
// It is a sibling of GET /api/bloem/v1/capabilities rather than a part of it. The
// capability document is a build constant that must never fail; identity
// resolves through the database and legitimately answers 503, and its
// setup_complete flips once, so it is served no-store. Folding one into the
// other would either make the capability probe fallible or make the identity
// fields silently absent — the ambiguity this endpoint exists to remove.
//
// Relationship to GET /api/v1/health: none. Health keeps exactly the fields it
// has and exactly the source it has — server_name and server_id there come from
// the Jellyfin-compatibility configuration and stay omitted when that
// configuration is absent. Nothing reads the identity setting into health,
// because clients (Jellyfin-protocol clients especially) already depend on the
// values health returns today, and the v1 rules forbid repurposing them. Scope
// keying uses this endpoint instead, which is why it exists.
type ServerIdentityHandler struct {
	identity *serverid.Resolver
	branding *branding.Service
	setup    SetupStateReporter
}

// NewServerIdentityHandler constructs the identity handler around the shared
// identifier resolver. The resolver is shared rather than owned so that every
// surface publishing the identifier — this endpoint, the local-network
// advertisement — resolves the same value from the same place. setup is
// required for a truthful answer and is nil only where the database is absent
// (test and fixture wiring); branding is optional and falls back to the default
// server name.
func NewServerIdentityHandler(
	identity *serverid.Resolver,
	brandingSvc *branding.Service,
	setup SetupStateReporter,
) *ServerIdentityHandler {
	return &ServerIdentityHandler{identity: identity, branding: brandingSvc, setup: setup}
}

// serverIdentityResponse is the body of GET /api/bloem/v1/server/identity.
//
// status is carried so the body also satisfies the client contract's
// ServerIdentity schema, which requires it; the rest is what scope keying
// needs. Fields are additive-only from here.
type serverIdentityResponse struct {
	Status        string   `json:"status"`
	ServerID      string   `json:"server_id"`
	ServerName    string   `json:"server_name"`
	APIVersions   []int    `json:"api_versions"`
	BloemAPI      []string `json:"bloem_api"`
	SetupComplete bool     `json:"setup_complete"`
}

// HandleGetServerIdentity answers the public identity probe.
//
// Every way this can fail ends in 503 with no identifier in the body. Inventing
// one — a per-process value, or a fresh value per request — is strictly worse
// than a client falling back to its origin string, because it silently re-keys
// state the client has already stored against this server.
func (h *ServerIdentityHandler) HandleGetServerIdentity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.identity == nil || h.setup == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Server identity is not available")
		return
	}

	serverID, err := h.identity.Resolve(ctx)
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
		BloemAPI:      append([]string(nil), nativeAPISurfaces...),
		SetupComplete: !needsSetup,
	})
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
