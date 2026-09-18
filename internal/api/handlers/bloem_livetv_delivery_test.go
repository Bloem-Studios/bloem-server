package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

func TestBloemLiveTVDeliveryURLNeverForwardsBridgeCredentials(t *testing.T) {
	h := NewBloemLiveTVHandler(livetv.NewService(nil), "fixture-secret")
	for _, tc := range []struct {
		name, bridgeURL string
		hls             bool
	}{
		{"local HLS", "/api/v1/livetv/live-hls/playback/index.m3u8", true},
		{"credential query", "/api/v1/livetv/live-hls/playback/index.m3u8?token=account-secret&profile_token=pin-secret&st=foreign", true},
		{"foreign origin", "https://foreign.invalid/api/v1/livetv/live-hls/playback/index.m3u8", false},
		{"protocol relative", "//foreign.invalid/api/v1/livetv/live-hls/playback/index.m3u8", false},
		{"wrong session", "/api/v1/livetv/live-hls/other/index.m3u8", false},
		{"wrong path", "/api/v1/playback/transcode/playback/master.m3u8", false},
		{"tuner", "http://192.0.2.1/auto/v1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := h.bloemSessionDeliveryURL(&livetv.LiveSession{ID: "live", PlaybackSessionID: "playback", HLSURL: tc.bridgeURL}, 7, "profile")
			u, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			if u.IsAbs() || u.Host != "" || u.User != nil || strings.Contains(got, "account-secret") || strings.Contains(got, "pin-secret") {
				t.Fatal("bridge authority or credentials escaped into the delivery URL")
			}
			if !tc.hls {
				if got != "/api/bloem/v1/livetv/sessions/live/stream" {
					t.Fatalf("unsafe bridge must fall back to the native proxy: %s", u.Path)
				}
				return
			}
			if u.Path != "/api/bloem/v1/livetv/live-hls/playback/index.m3u8" || len(u.Query()) != 1 {
				t.Fatalf("noncanonical native HLS URL: %s", u.Path)
			}
			claims, err := streamtoken.Verify(u.Query().Get("st"), "fixture-secret")
			if err != nil || claims.SessionID != "playback" || claims.UserID != 7 || claims.ProfileID != "profile" || claims.PlayMethod != "bloem_livetv_hls" {
				t.Fatalf("delivery proof lost its purpose or owner: %v", err)
			}
		})
	}
}

func TestBloemLiveTVStartFailsClosedWithoutSigning(t *testing.T) {
	h := NewBloemLiveTVHandler(livetv.NewService(nil), "")
	w := httptest.NewRecorder()
	h.HandleStartChannelSession(w, httptest.NewRequest(http.MethodPost, "/api/bloem/v1/livetv/channels/channel/session", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", w.Code)
	}
}

func TestBloemLiveTVPlaylistDoesNotSignForeignOrigins(t *testing.T) {
	playlist := "#EXTM3U\nseg_00000.ts\nhttps://foreign.invalid/segment.ts\n//foreign.invalid/segment.ts\n"
	got := rewriteHLSPlaylistAuthQuery(playlist, "st=fixture-ticket")
	if !strings.Contains(got, "seg_00000.ts?st=fixture-ticket\n") || !strings.Contains(got, "https://foreign.invalid/segment.ts\n") || !strings.Contains(got, "//foreign.invalid/segment.ts\n") {
		t.Fatal("playlist ticket propagation escaped the local origin")
	}
}
