package handlers

import (
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// bloemStreamTokenAuthorized reports whether a claimless stream request
// carries a verified session-bound stream token. That token IS the
// authorization for this delivery path: playback start returns a stream_url
// carrying one precisely because native players cannot attach a bearer to
// every range request. Refusing the claimless request the server itself
// issued made that URL unusable as designed.
//
// On the same path, LoadOrReconstructSession authorizes on the account. A
// session or restart recipe names a source; it does not preserve a library entitlement, so a revoked library is
// treated the same as a missing file, and an inaccessible sidecar source is
// indistinguishable from a missing session.
func bloemStreamTokenAuthorized(r *http.Request) bool {
	return apimw.IsStreamTokenAuthorized(r.Context())
}

// validHomeSurface (home_dismissals.go) also accepts the S-2 promo surfaces
// ("promo:home", "promo:detail", "promo:pre_playback"), whose item ids are
// promotion ids (per-item "don't show this again").
