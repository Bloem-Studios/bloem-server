package handlers

import (
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
