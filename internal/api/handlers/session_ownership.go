package handlers

import (
	"context"
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// callerOwnsPlaybackSession reports whether the authenticated caller may act on
// a playback session.
//
// The account check alone was sufficient while every session on an account was
// interchangeable: each of a household's profiles shares one login, so
// "belongs to my account" and "belongs to me" were the same statement. Direct
// profile login breaks that. Such a session authenticates one profile and is
// narrower than the account behind it, so a sibling's session is somebody
// else's even though the two share a user_id — and a playback session id is a
// bearer for progress, stop, control, and media delivery.
//
// Callers that authenticate by session id alone (native players following a
// signed delivery URL, which carry no claims) keep working: with no
// direct-profile claim to compare against, this is the account check it has
// always been.
func callerOwnsPlaybackSession(r *http.Request, sessionUserID int, sessionProfileID string, callerUserID int) bool {
	if sessionUserID != callerUserID {
		return false
	}
	claims := apimw.GetClaims(r.Context())
	if claims == nil || claims.AuthMethod != auth.AuthMethodDirectProfile {
		return true
	}
	return claims.ProfileID != "" && sessionProfileID == claims.ProfileID
}

// bloemStreamTokenAuthorized reports whether a claimless stream request
// carries a verified session-bound stream token. That token IS the
// authorization for this delivery path: playback start returns a stream_url
// carrying one precisely because native players cannot attach a bearer to
// every range request. Refusing the claimless request the server itself
// issued made that URL unusable as designed.
//
// On the same path, LoadOrReconstructSession authorizes on the account; a
// direct-profile bearer is narrower than its account and gets its own session
// only (callerOwnsPlaybackSession). A session or restart recipe names a
// source; it does not preserve a library entitlement, so a revoked library is
// treated the same as a missing file, and an inaccessible sidecar source is
// indistinguishable from a missing session.
func bloemStreamTokenAuthorized(r *http.Request) bool {
	return apimw.IsStreamTokenAuthorized(r.Context())
}

// bloemDirectProfileClaims reports whether the request authenticated as a
// direct profile session, which may never manage the household.
func bloemDirectProfileClaims(ctx context.Context) bool {
	claims := apimw.GetClaims(ctx)
	return claims != nil && claims.AuthMethod == auth.AuthMethodDirectProfile
}

// validHomeSurface (home_dismissals.go) also accepts the S-2 promo surfaces
// ("promo:home", "promo:detail", "promo:pre_playback"), whose item ids are
// promotion ids (per-item "don't show this again").
