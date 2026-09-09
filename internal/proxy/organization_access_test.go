package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

func TestProxyMediaRechecksLibraryAccess(t *testing.T) {
	path := writeGrantMedia(t, "authorized-bytes")
	card := playback.RecipeCard{SessionID: "session-1", UserID: 7, ProfileID: "profile-1", MediaFileID: 42, PlayMethod: playback.PlayDirect, InputPath: path}
	srv := newGrantProxyServer(t, map[string]playback.RecipeCard{"session-1": card})
	allowed := true
	var lookupErr error
	srv.SetMediaLibraryAccess(func(_ context.Context, userID, fileID int) (bool, error) {
		if userID != 7 || fileID != 42 {
			t.Fatalf("wrong authority lookup: %d %d", userID, fileID)
		}
		return allowed, lookupErr
	})
	token, err := streamtoken.Sign(card.ToClaims(), grantTestSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	download := card.ToClaims()
	download.PlayMethod = streamtoken.PlayMethodDownload
	downloadToken, err := streamtoken.Sign(download, grantTestSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	routes := []string{"/stream/direct/" + token, "/stream/v3/session-1", "/downloads/file/" + downloadToken}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, route := range routes {
			response := grantRequest(t, srv, method, route, grantAccessToken(t, 7, "login-1"))
			if response.Code != 200 {
				t.Fatalf("authorized %s: %d %s", method, response.Code, response.Body.String())
			}
		}
	}
	for _, state := range []string{"revoked", "database unavailable"} {
		t.Run(state, func(t *testing.T) {
			allowed = false
			if state == "database unavailable" {
				lookupErr = errors.New("database unavailable")
			}
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, route := range routes {
					response := grantRequest(t, srv, method, route, grantAccessToken(t, 7, "login-1"))
					want := 404
					if lookupErr != nil {
						want = 503
					}
					if response.Code != want {
						t.Fatalf("denied %s: %d %s, want %d", method, response.Code, response.Body.String(), want)
					}
					wantType := "text/plain"
					if strings.HasPrefix(route, "/stream/v3/") {
						wantType = "application/json"
					}
					if !strings.HasPrefix(response.Header().Get("Content-Type"), wantType) {
						t.Fatalf("%s error encoding: %q, want %s", route, response.Header().Get("Content-Type"), wantType)
					}
				}
			}
		})
	}
}
