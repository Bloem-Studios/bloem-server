package handlers

import (
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// The stream_url playback returns is the whole native-player contract: those
// players cannot attach a bearer to range requests, so the URL must carry a
// verified session-bound token or the claimless delivery path has nothing to
// authorize. This pins the emission end of that contract; the delivery end is
// covered by the router-level stream token test.
func TestPlaybackStreamURLCarriesASessionBoundToken(t *testing.T) {
	const secret = "stream-url-token-secret"
	handler := &PlaybackHandler{JWTSecret: secret}
	session := &playback.Session{
		ID:          "session-under-test",
		UserID:      7,
		ProfileID:   "reader",
		MediaFileID: 42,
		PlayMethod:  playback.PlayDirect,
	}

	url := handler.playbackStreamURL(session)
	if !strings.HasPrefix(url, "/stream/session-under-test?st=") {
		t.Fatalf("stream URL = %q, want the progressive path with an st token", url)
	}
	token := strings.TrimPrefix(url, "/stream/session-under-test?st=")
	claims, err := streamtoken.Verify(token, secret)
	if err != nil {
		t.Fatalf("verify emitted token: %v", err)
	}
	if claims.SessionID != session.ID {
		t.Fatalf("token session = %q, want it bound to %q", claims.SessionID, session.ID)
	}

	// With no signing secret the URL is bare — reconstruct-off deployments
	// keep working, they just require bearer-authenticated delivery.
	bare := (&PlaybackHandler{}).playbackStreamURL(session)
	if bare != "/stream/session-under-test" {
		t.Fatalf("unsigned stream URL = %q, want it bare", bare)
	}
}
