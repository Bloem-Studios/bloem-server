package api

import (
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// sessionIDFromStreamToken returns the playback session a stream token was
// minted for. It refuses a token that does not verify under secret, and one
// that is not a playback session's (a download, theme song or probe token).
func sessionIDFromStreamToken(token, secret string) (string, bool) {
	if token == "" {
		return "", false
	}
	claims, err := streamtoken.Verify(token, secret)
	if err != nil || !claims.AuthorizesPlaybackMedia() {
		return "", false
	}
	return claims.SessionID, true
}

// mediaRequestSessionID names the playback session a media request belongs
// to. A delivery path names its session itself, which covers
// header-authenticated media that carries no stream token; a token on that
// path must name the same session. Elsewhere only a playback stream token
// names one. The caller records anything only after the request was
// authorized, so a path alone never writes facts for someone else's session.
func mediaRequestSessionID(path, streamToken, secret string) (string, bool) {
	if pathID, ok := middleware.DeliverySessionID(path); ok {
		if streamToken == "" {
			return pathID, true
		}
		if tokenID, ok := sessionIDFromStreamToken(streamToken, secret); ok && tokenID == pathID {
			return pathID, true
		}
		return "", false
	}
	return sessionIDFromStreamToken(streamToken, secret)
}
