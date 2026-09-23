package apiv2

// Bloem scoped stream-token delivery coverage moved out of Silo's
// playback_delivery_test.go.

import (
	"net/http"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

func TestPlaybackDeliveryV2ScopedStreamToken(t *testing.T) {
	deps, _ := catalogDeps(t)
	const secret = "v2-scoped-stream-test"
	deps.StreamTokens = deps.Auth.StreamTokenAuth(secret)
	calls := 0
	deps.PlaybackMedia = &PlaybackMediaHandlers{Original: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) })}
	h := newTestHandler(t, deps)
	// UserID is a real recipe-card field on every token production mints (see
	// playback.NewDirectRecipeCard and friends); RequireViewerAccess now uses
	// it as a lookup key to resolve a real scope for a bearer-less request
	// instead of skipping, so a synthetic token in this test needs one too.
	token, err := streamtoken.Sign(streamtoken.Claims{SessionID: deliveryTestSession, UserID: 1}, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	path := Prefix + "/stream/" + deliveryTestSession + "?st=" + token
	for _, method := range []string{"GET", "HEAD"} {
		rec := do(t, h, method, path, "", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("scoped delivery %s: %d %s", method, rec.Code, rec.Body)
		}
	}
	before := calls
	requireProblem(t, do(t, h, "GET", Prefix+"/stream/22222222-2222-4222-8222-222222222222?st="+token, "", nil), TypeAuthenticationRequired)
	requireProblem(t, do(t, h, "GET", Prefix+"/profiles?st="+token, "", nil), TypeAuthenticationRequired)
	if calls != before {
		t.Fatal("stream token escaped its session")
	}
}
