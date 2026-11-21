package apiv2

import (
	"context"
	"net/http"
	"testing"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// Silo's TestPlaybackDeliveryAcceptsStreamReferenceAlone, run with Bloem's
// stream-token gate wired in front of the delivery routes as production wires
// it (router_bloem_wiring.go). Silo's apps read a plan's stream URL, whose st
// is the stream reference both gates verify, so they must get Silo's answers:
// a stopped attempt's reference is refused with playback_session_ended, and
// one whose login session has ended is refused as a credential.
func TestBloemStreamTokenGateKeepsSiloStreamReferenceAnswers(t *testing.T) {
	const secret = "stream-reference-secret"
	const signedOut = "44444444-4444-4444-8444-444444444444"
	deps, _ := catalogDeps(t)
	deps.Auth.SetStreamReferenceVerifier(func(reference string) (*streamtoken.Claims, error) {
		return streamtoken.Verify(reference, secret)
	}, func(_ context.Context, sessionID string) (apimw.StreamReferenceAttempt, error) {
		switch sessionID {
		case deliveryTestSession:
			// s1 is the member's active login session in the shared test fakes.
			return apimw.StreamReferenceAttempt{UserID: 1, ProfileID: "p-owner", LoginSessionID: "s1", Live: true}, nil
		case signedOut:
			return apimw.StreamReferenceAttempt{UserID: 1, ProfileID: "p-owner", LoginSessionID: "signed-out", Live: true}, nil
		}
		return apimw.StreamReferenceAttempt{}, nil
	})
	deps.StreamTokens = deps.Auth.StreamTokenAuth(secret)
	served := 0
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.WriteHeader(http.StatusOK)
	})
	deps.PlaybackMedia = &PlaybackMediaHandlers{Original: ok, Manifest: ok, Segment: ok, Subtitle: ok}
	h := newTestHandler(t, deps)
	sign := func(session string) string {
		t.Helper()
		reference, err := streamtoken.Sign(streamtoken.Claims{SessionID: session, PlayMethod: "transcode", UserID: 1, ProfileID: "p-owner"}, secret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return reference
	}
	manifest := func(session string) string {
		return Prefix + "/playback/transcode/" + session + "/master.m3u8?st=" + sign(session)
	}

	if rec := do(t, h, http.MethodGet, manifest(deliveryTestSession), "", nil); rec.Code != http.StatusOK || served != 1 {
		t.Fatalf("live attempt: %d (served %d) %s", rec.Code, served, rec.Body.String())
	}
	served = 0
	const ended = "33333333-3333-4333-8333-333333333333"
	requireProblem(t, do(t, h, http.MethodGet, manifest(ended), "", nil), TypePlaybackSessionEnded)
	requireProblem(t, do(t, h, http.MethodGet, Prefix+"/stream/"+ended+"?st="+sign(ended), "", nil), TypePlaybackSessionEnded)
	if rec := do(t, h, http.MethodGet, manifest(signedOut), "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out login session: %d, want 401; %s", rec.Code, rec.Body.String())
	}
	if served != 0 {
		t.Fatalf("served %d refused references", served)
	}
}
