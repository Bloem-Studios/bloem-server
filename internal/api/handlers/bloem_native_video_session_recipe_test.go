package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// A header-authenticated native URL carries no stream token, so the session
// recipe is the only thing a restarted process can rebuild the session from.
// It must name the native file so recovery runs the native authority check.
func TestNativeVideoHeaderAuthenticatedTransportKeepsSessionRecipe(t *testing.T) {
	for _, delivery := range []playback.DeliveryV3{playback.DeliveryOriginalHTTPV3, playback.DeliveryRemuxProgressiveV3} {
		for _, headerAuth := range []bool{false, true} {
			t.Run(string(delivery)+map[bool]string{false: "/token", true: "/header"}[headerAuth], func(t *testing.T) {
				service, file, session, _, _ := nativeVideoFixture(t)
				h := NewPlaybackHandler(service.Sessions, service.Files)
				h.NativeVideo, h.JWTSecret = service, "fixture-secret"
				method := playback.PlayDirect
				if delivery == playback.DeliveryRemuxProgressiveV3 {
					method = playback.PlayRemux
				}
				result := playback.PlannerResultV3{PlayMethod: method, Plan: &playback.PlanV3{EffectiveMediaFileID: file.ID, Delivery: delivery}}
				transport, failure := h.prepareNativeVideoTransportV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, mediaAuthModeV3{headerAuth: headerAuth}, config.DefaultPlaybackRoutingPolicy())
				if failure != nil {
					t.Fatal(failure)
				}
				defer transport.rollback()
				if strings.Contains(transport.url, streamTokenParam+"=") == headerAuth {
					t.Fatalf("url=%q", transport.url)
				}
				card := transport.sessionRecipe
				if !headerAuth {
					if card != nil {
						t.Fatalf("token transport stored a recipe: %+v", card)
					}
					return
				}
				if card == nil || card.SessionID != session.ID || card.UserID != session.UserID || card.ProfileID != session.ProfileID || card.MediaFileID != file.ID || card.InputPath != file.FilePath || card.PlayMethod != method {
					t.Fatalf("session recipe=%+v", card)
				}
			})
		}
	}
}
