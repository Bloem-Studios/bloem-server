package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
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

// The production composition path: prepareIdentityTransportV3 is what turns a
// planned direct/remux session into the transport URL the playback response
// carries. If it stopped signing, the helper test above would still pass while
// every native player broke, so this pins the composed URL itself.
func TestPrepareIdentityTransportV3EmitsASignedStreamURL(t *testing.T) {
	const secret = "transport-compose-secret"
	handler := NewPlaybackHandler(nil)
	handler.JWTSecret = secret
	session := &playback.Session{
		ID:          "compose-session",
		UserID:      7,
		ProfileID:   "reader",
		MediaFileID: 42,
		PlayMethod:  playback.PlayDirect,
	}
	result := playback.PlannerResultV3{
		Plan:       &playback.PlanV3{EffectiveMediaFileID: 42, Delivery: playback.DeliveryOriginalHTTPV3},
		PlayMethod: playback.PlayDirect,
	}

	r := httptest.NewRequest("GET", "/", nil)
	file := &models.MediaFile{ID: 42}
	transport, transportErr := handler.prepareIdentityTransportV3(r, session, file, result, preparedTimelineV3{})
	if transportErr != nil {
		t.Fatalf("prepareIdentityTransportV3: %v", transportErr)
	}
	defer transport.rollback()

	if !strings.HasPrefix(transport.url, "/stream/compose-session?st=") {
		t.Fatalf("transport URL = %q, want a signed progressive URL", transport.url)
	}
	claims, err := streamtoken.Verify(strings.TrimPrefix(transport.url, "/stream/compose-session?st="), secret)
	if err != nil {
		t.Fatalf("verify composed token: %v", err)
	}
	if claims.SessionID != session.ID {
		t.Fatalf("composed token session = %q, want %q", claims.SessionID, session.ID)
	}
}
