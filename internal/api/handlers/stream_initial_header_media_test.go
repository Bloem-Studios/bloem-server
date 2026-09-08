package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestInitialHeaderSubtitleHTTP(t *testing.T) {
	h, _, _, grants := boundSubtitleFixture(t)
	session, err := h.sessionMgr.GetSession("logical")
	if err != nil {
		t.Fatal(err)
	}
	session.RequireMediaAuthorization = true
	// Register the updated captured transport mode using the existing manager.
	if err := h.sessionMgr.(*playback.SessionManager).UpdateStreamState(session.ID, playback.SessionStreamState{MediaAuthorizationSet: true, RequireMediaAuthorization: true}); err != nil {
		t.Fatal(err)
	}
	router := boundSubtitleRouter(h)
	for _, route := range []string{"0.vtt"} {
		for _, profile := range []string{"profile", "wrong", ""} {
			req := httptest.NewRequest(http.MethodGet, "/stream/logical/subtitles/"+route+"?file_id=42", nil)
			req.Header.Set("Authorization", "Bearer fixture-access")
			req.Header.Set("X-Profile-Id", profile)
			before := *grants
			rec := httptest.NewRecorder()
			router.ServeHTTP(&nativeGrantRecorder{rec}, req)
			if profile == "profile" {
				if rec.Code != 200 {
					t.Fatalf("%s %d %s", route, rec.Code, rec.Body.String())
				}
			} else if rec.Code == 200 || *grants != before {
				t.Fatal("wrong authority served")
			}
		}
	}
	if *grants == 0 {
		t.Fatalf("grant count %d", *grants)
	}
}
