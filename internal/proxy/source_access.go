package proxy

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// A signed recipe, media grant, or download URL all name a source media
// file; none of them preserve a library entitlement. This node has no
// request-scoped viewer scope of its own to recheck against (that lives on
// the API, which resolves it per request from the caller's organization
// membership), so it must ask an authority directly whether the account
// behind the request may still reach that source before serving any byte.
//
// SourceAccess is an injected interface in the same shape as
// proxyGrantLookup and loginSessionValidator (see mediagrant.go): NewServer
// takes no database handle (there is no pgxpool.Pool or sql.DB anywhere in
// this package), so the concrete, DB-backed implementation is built outside
// internal/proxy — in cmd/silo, where the pool this process opened already
// exists — and wired in with SetSourceAccess. This package must never import
// internal/tenancy or internal/resourcetenancy directly.
//
// The method takes a media file ID, not a media folder ID: nothing that
// reaches this package ever carries a resolved media_folder_id.
// streamtoken.Claims and playback.RecipeCard both stop at MediaFileID, so
// the concrete implementation is the one that resolves the file's current
// library before checking organization entitlement.
type SourceAccess interface {
	AllowMediaSource(ctx context.Context, accountID int, mediaFileID int) error
}

// ErrSourceHidden means the source exists but this account may no longer
// reach it — its library was reassigned, unshared with the organization, or
// the organization itself is hidden or suspended. Callers answer 404, never
// confirming the source's existence to a caller who has lost access to it.
var ErrSourceHidden = errors.New("media source hidden from this account")

// ErrAuthorityUnavailable means the recheck could not reach a conclusive
// answer (no wired authority, a query failure, an unresolvable tenant).
// Callers answer 503 rather than guess and serve bytes anyway.
var ErrAuthorityUnavailable = errors.New("media source authority unavailable")

// checkSourceAccess rechecks claims against the current source-access
// authority before this proxy serves a byte from claims.MediaPath, or relays
// a request to a transcode node on the caller's behalf. It writes the
// response directly and reports whether the caller may proceed.
//
// A nil sourceAccess is deliberately NOT "allow": mirroring the documented
// behavior for nil grants/loginSessions (server.go:50-58), a proxy this
// dependency was never wired for cannot verify anything locally, so it
// answers 503 rather than assume authorization. Every call site is one of
// the account-scoped media routes in the egress-metered route group; the
// shared-node-secret admin/worker routes (requireBearer) never call this.
func (s *Server) checkSourceAccess(w http.ResponseWriter, r *http.Request, claims *streamtoken.Claims) bool {
	if s.sourceAccess == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return false
	}
	switch err := s.sourceAccess.AllowMediaSource(r.Context(), claims.UserID, claims.MediaFileID); {
	case err == nil:
		return true
	case errors.Is(err, ErrSourceHidden):
		http.NotFound(w, r)
		return false
	default:
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return false
	}
}
