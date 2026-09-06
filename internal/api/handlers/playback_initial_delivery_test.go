package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/go-chi/chi/v5"
)

func TestInitialPlaybackDeliveryRejectsUnboundAuthority(t *testing.T) {
	const secret = "synthetic-test-secret"
	card := playback.NewDirectRecipeCard("session", 1, "profile", 42)
	token, err := streamtoken.Sign(card.ToClaims(), secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfigured", true: "legacy-card"}[configured], func(t *testing.T) {
			h := &PlaybackHandler{JWTSecret: secret}
			if configured {
				h.initialFlow = &InitialPlaybackFlowV3{}
			}
			router := chi.NewRouter()
			router.Handle("/stream/{session_id}", h.InitialPlaybackDelivery(func(http.ResponseWriter, *http.Request) { t.Fatal("unbound authority reached delivery") }))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/stream/session?st="+token, nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
}
