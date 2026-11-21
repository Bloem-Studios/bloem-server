package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// streamTokenAuthorizedKey marks a request as authorized by a signed stream
// token rather than by a session bearer.
const streamTokenAuthorizedKey contextKey = "stream_token_authorized"

// streamTokenClaimsKey carries the verified token's claims for the one
// downstream consumer that needs them as lookup keys: RequireViewerAccess,
// which re-resolves a fresh, organization-bounded scope from
// uid/pid rather than trusting anything else the token carries.
const streamTokenClaimsKey contextKey = "stream_token_claims"

// HLS delivery lives at:
//
//	/api/v2/playback/transcode/{session_id}/master.m3u8
//	/api/v2/playback/transcode/{session_id}/media.m3u8
//	/api/v2/playback/transcode/{session_id}/segment/{name}
//
// Matched on the raw path rather than chi URL params on purpose: middleware
// registered on the parent group runs before the inner route is matched, so
// chi.URLParam is still empty here. (That is also why a rejected request logs
// path_pattern=/api/v2/playback/* instead of the real pattern.)
const transcodeDeliveryPrefix = "/playback/transcode/"

// Progressive (direct-play / remux) delivery lives at:
//
//	/api/v2/stream/{session_id}
//
// A different prefix entirely, which is why it needs its own case: the token was
// already minted for it (playbackStreamURL appends one to every non-transcode
// stream_url) but nothing here honored it, so a native player following that URL
// got a 401 on every attempt and reported a connection failure.
const progressiveDeliveryPrefix = "/stream/"

// apiVersionPrefix anchors the progressive match so "/stream/" is only honored
// at the top level of the API, never as a nested segment of another route.
const apiVersionPrefix = "/api/v2"

// Live TV HLS delivery lives at:
//
//	/api/bloem/v2/livetv/live-hls/{playback_session_id}/index.m3u8
//	/api/bloem/v2/livetv/live-hls/{playback_session_id}/seg_00000.ts
//
// Same disease as the progressive path above, one route later. The app embeds
// its own access token in this URL and hands it to the native player, which
// cannot refresh or retry -- so the moment that token goes stale the player
// polls 401 forever while the app's own requests sail through on refresh. A live
// session outlives a short access token by design, which makes a rotating
// bearer the wrong credential for it.
const liveHLSDeliveryPrefix = "/livetv/live-hls/"

// streamTokenDeliverySession returns the session ID a request is asking to be
// delivered, and whether the path is a media delivery path at all.
//
// Only these delivery shapes qualify. Everything else — including the mutation
// routes under /playback — keeps requiring a session bearer, so a stream token
// can never be spent as a general-purpose credential.
//
// media.m3u8 has to be here for the same reason master.m3u8 is: the player
// follows the master's variant URI itself, so that fetch carries the stream
// token and no bearer. Omitting it 401s every variant fetch, which is exactly
// what happened when the master playlist landed without this list being updated.
func streamTokenDeliverySession(urlPath string) (string, bool) {
	if sessionID, ok := transcodeDeliverySession(urlPath); ok {
		return sessionID, true
	}
	if sessionID, ok := liveHLSDeliverySession(urlPath); ok {
		return sessionID, true
	}
	return progressiveDeliverySession(urlPath)
}

// liveHLSDeliverySession matches a Live TV playlist or segment fetch.
//
// The playlist and its segments both have to qualify, for the same reason
// media.m3u8 does above: serveLiveHLSPlaylist copies the request query onto every
// relative media URI, so the player's segment fetches carry the stream token and
// no bearer. Authorizing only the playlist would 401 every segment.
//
// The name is confined to a single path segment so a token for one session cannot
// be spent walking out of its own directory.
func liveHLSDeliverySession(urlPath string) (string, bool) {
	var rest string
	for _, prefix := range []string{"/api/bloem/v2", ""} {
		if strings.HasPrefix(urlPath, prefix+liveHLSDeliveryPrefix) {
			rest = strings.TrimPrefix(urlPath, prefix+liveHLSDeliveryPrefix)
			break
		}
	}
	if rest == "" {
		return "", false
	}
	sessionID, name, found := strings.Cut(rest, "/")
	if !found || sessionID == "" || name == "" {
		return "", false
	}
	if strings.Contains(name, "/") || name == "." || name == ".." || sessionID == "." || sessionID == ".." {
		return "", false
	}
	return sessionID, true
}

func transcodeDeliverySession(urlPath string) (string, bool) {
	idx := strings.Index(urlPath, transcodeDeliveryPrefix)
	if idx < 0 {
		return "", false
	}
	rest := urlPath[idx+len(transcodeDeliveryPrefix):]
	sessionID, tail, found := strings.Cut(rest, "/")
	if !found || sessionID == "" {
		return "", false
	}
	switch {
	case tail == "master.m3u8", tail == "media.m3u8":
		return sessionID, true
	case strings.HasPrefix(tail, "segment/") && len(tail) > len("segment/"):
		// A segment name may not climb out of the segment namespace.
		if strings.Contains(strings.TrimPrefix(tail, "segment/"), "/") {
			return "", false
		}
		return sessionID, true
	default:
		return "", false
	}
}

// progressiveDeliverySession matches the bare progressive stream path.
//
// Only the bare path: the session's own sub-resources (subtitles, fonts) are
// deliberately excluded, because a stream token authorizes delivery of the
// session's media bytes and nothing wider. Widening it later is a decision to
// make on purpose rather than by accident of prefix matching.
//
// The prefix is anchored rather than searched. Searching for "/stream/"
// anywhere would also match /api/v2/playback/transcode/{id}/stream/... and any
// future route containing that segment, quietly extending what a stream token
// authorizes.
func progressiveDeliverySession(urlPath string) (string, bool) {
	idx := strings.Index(urlPath, progressiveDeliveryPrefix)
	if idx < 0 {
		return "", false
	}
	// Must be a top-level /stream/, not a nested segment of some other route.
	//
	// An empty prefix counts: chi trims the mount prefix for sub-routers, so this
	// middleware sees "/stream/{id}" as often as "/api/v2/stream/{id}" -- which is
	// also why the transcode matcher searches instead of anchoring. Requiring the
	// API prefix rejected every real request and 401'd the player.
	if before := urlPath[:idx]; before != "" && !strings.HasSuffix(before, apiVersionPrefix) {
		return "", false
	}
	sessionID := urlPath[idx+len(progressiveDeliveryPrefix):]
	if sessionID == "" || strings.Contains(sessionID, "/") {
		return "", false
	}
	return sessionID, true
}

// StreamTokenAuth authorizes HLS delivery requests that carry a valid signed
// stream token, so the URL is self-authorizing as designed.
//
// The player, not the app, fetches the playlist and every segment. On a TV that
// is AVPlay, which cannot attach an Authorization header or refresh an expired
// access token — which is why the server mints "st" (a session-scoped, signed
// descriptor) and threads it through manifests in the first place. Until now
// nothing at the auth layer honored it: RequireAuth ran first and rejected the
// request before the handler ever read the token, so delivery depended on an
// access token pasted into the URL. A stale or empty one produced an endless
// 401 poll that looks exactly like a transcode that never becomes ready.
//
// This middleware only ever *adds* authorization: it marks the request and lets
// RequireAuth and RequireViewerAccess skip it. A request that already passes
// bearer auth is untouched, so no currently-working request changes behavior;
// only requests that would have 401'd can now succeed, and only for the two
// delivery paths, and only with a signature that names their own session.
// It adds nothing once the playback's login session has ended
// (streamTokenLoginSessionLive), so signing out, a revoked session and a
// disabled account end the media URL as they do on Silo. Nor does it when the
// request carries a valid account credential of its own (credentialAccount):
// Silo then decides by that credential, its declared profile and the
// handler's ownership check. An expired or malformed one is ignored, so a
// stale bearer sent beside the URL leaves the token to decide. Live TV
// delivery is outside both rules.
func (am *AuthMiddleware) StreamTokenAuth(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if secret == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.URL.Query().Get(streamtoken.QueryParam)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			sessionID, ok := streamTokenDeliverySession(r.URL.Path)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			claims, err := streamtoken.Verify(token, secret)
			// Binding the signature to the session in the path is the whole
			// authorization: a token minted for one session must not fetch
			// another's bytes. Verify already rejects a bad signature or an
			// expired token.
			if err != nil || claims == nil || claims.SessionID == "" || claims.SessionID != sessionID {
				next.ServeHTTP(w, r)
				return
			}
			if _, liveTV := liveHLSDeliverySession(r.URL.Path); !liveTV {
				account, err := am.credentialAccount(r)
				if err != nil {
					writeCredentialCheckUnavailable(w, r, err)
					return
				}
				if account > 0 {
					// Silo decides a request with a valid account credential
					// by that credential (RequireStreamReferenceAuth): its
					// declared profile's proof and the handler's ownership
					// check answer, not the URL token.
					next.ServeHTTP(w, r)
					return
				}
			}
			live, err := am.streamTokenLoginSessionLive(r.Context(), claims)
			if err != nil {
				writeCredentialCheckUnavailable(w, r, err)
				return
			}
			if !live {
				// Silo's stream reference gate or the account credential decides.
				next.ServeHTTP(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), streamTokenAuthorizedKey, true)
			ctx = context.WithValue(ctx, streamTokenClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// credentialAccount returns the account a request's own credential names
// (bearer header, `token` query or API key), or 0 when it carries none or the
// credential does not validate: an expired or malformed bearer leaves the
// stream token to decide, as it did before. An error means an API key could
// not be checked.
func (am *AuthMiddleware) credentialAccount(r *http.Request) (int, error) {
	token, ok := extractBearerToken(r)
	if !ok {
		return 0, nil
	}
	if strings.HasPrefix(token, "sa_") {
		if am.apiKeyValidator == nil {
			return 0, nil
		}
		key, err := am.apiKeyValidator.GetByKey(r.Context(), token)
		if errors.Is(err, auth.ErrAPIKeyNotFound) || (err == nil && key == nil) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		return key.UserID, nil
	}
	if am.tokenValidator == nil {
		return 0, nil
	}
	// An invalid or expired token names no account: the stream token decides.
	if claims, err := am.tokenValidator.ValidateToken(token); err == nil && claims != nil && claims.TokenType == auth.TokenTypeAccess {
		return claims.UserID, nil
	}
	return 0, nil
}

// streamTokenLoginSessionLive applies Silo's stream reference rule
// (RequireStreamReferenceAuth) to a token StreamTokenAuth verified: a playback
// attempt's token stops standing in for the account once the login session
// that started the attempt has ended, and an attempt started without one (an
// API key) needs the account credential. Without the attempt and session
// sources there is nothing to check. A session with no v3 playback attempt,
// such as a Live TV stream, keeps the token as before.
func (am *AuthMiddleware) streamTokenLoginSessionLive(ctx context.Context, claims *streamtoken.Claims) (bool, error) {
	if am.streamReferenceSession == nil || am.sessionValidator == nil {
		return true, nil
	}
	attempt, err := am.streamReferenceSession(ctx, claims.SessionID)
	if err != nil {
		return false, err
	}
	if attempt.UserID == 0 {
		return true, nil
	}
	if attempt.LoginSessionID == "" {
		return false, nil
	}
	_, _, active, err := am.sessionAuthority(ctx, attempt.LoginSessionID, attempt.UserID)
	return active && err == nil, err
}

// IsStreamTokenAuthorized reports whether a verified, session-bound stream
// token already authorized this request.
//
// Deliberately carries no identity. The stream token's ownership claims
// (uid/pid/mfid) are lookup keys re-resolved against the authority on
// reconstruct and are never trusted on their own, so this grants exactly one
// thing — delivery of the named session's bytes — and never a user or profile.
func IsStreamTokenAuthorized(ctx context.Context) bool {
	authorized, _ := ctx.Value(streamTokenAuthorizedKey).(bool)
	return authorized
}

// StreamTokenClaims returns the verified claims of the stream token that
// authorized this request, when one did. RequireViewerAccess uses
// UserID/ProfileID from these claims purely as lookup keys to re-resolve a
// fresh viewer scope; see the type's own docs for why they are never trusted
// as an identity assertion on their own.
func StreamTokenClaims(ctx context.Context) (*streamtoken.Claims, bool) {
	claims, ok := ctx.Value(streamTokenClaimsKey).(*streamtoken.Claims)
	return claims, ok && claims != nil
}
