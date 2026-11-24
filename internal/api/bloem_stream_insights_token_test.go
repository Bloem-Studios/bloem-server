package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

const streamInsightsTestSecret = "stream-insights-test-secret-0123456789"

func mintStreamInsightsToken(t *testing.T, sessionID, method string) string {
	t.Helper()
	token, err := streamtoken.Sign(streamtoken.Claims{SessionID: sessionID, UserID: 7, ProfileID: "p1", PlayMethod: method}, streamInsightsTestSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// TestSessionIDFromEveryMediaURLForm passes one request of each media URL
// form through captureNativeVideoRequest and checks that stream insights
// can name the playback session it belongs to: from the delivery path, which
// every form carries, or from the stream token's sid where the path names no
// session.
func TestSessionIDFromEveryMediaURLForm(t *testing.T) {
	transcode := mintStreamInsightsToken(t, "sess-1", "transcode")
	direct := mintStreamInsightsToken(t, "sess-1", "direct")
	other := mintStreamInsightsToken(t, "sess-2", "transcode")
	download := mintStreamInsightsToken(t, "dl-1", streamtoken.PlayMethodDownload)

	cases := []struct {
		name   string
		target string
		want   string
		wantOK bool
	}{
		{"HLS master with token", "/api/v2/playback/transcode/sess-1/master.m3u8?st=" + transcode, "sess-1", true},
		{"HLS media playlist with token", "/api/v2/playback/transcode/sess-1/media.m3u8?st=" + transcode, "sess-1", true},
		{"HLS segment with token", "/api/v2/playback/transcode/sess-1/segment/seg_00001.m4s?st=" + transcode, "sess-1", true},
		{"direct file with token", "/api/v2/stream/sess-1?st=" + direct, "sess-1", true},
		{"Live TV playlist with token", "/api/bloem/v2/livetv/live-hls/sess-1/index.m3u8?st=" + transcode, "sess-1", true},
		{"Live TV segment with token", "/api/bloem/v2/livetv/live-hls/sess-1/seg_00000.ts?st=" + transcode, "sess-1", true},
		{"header-authenticated HLS master", "/api/v2/playback/transcode/sess-1/master.m3u8", "sess-1", true},
		{"header-authenticated HLS segment", "/api/v2/playback/transcode/sess-1/segment/seg_00001.m4s", "sess-1", true},
		{"header-authenticated direct file", "/api/v2/stream/sess-1", "sess-1", true},
		{"token on a session sub-resource", "/api/v2/stream/sess-1/subtitles/2?st=" + transcode, "sess-1", true},
		{"token for another session", "/api/v2/playback/transcode/sess-1/master.m3u8?st=" + other, "", false},
		{"download token", "/api/v2/items/abc?st=" + download, "", false},
		{"invalid token", "/api/v2/items/abc?st=not-a-token", "", false},
		{"no token and no media path", "/api/v2/items/abc", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotID string
			var gotOK, seen bool
			handler := captureNativeVideoRequest(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				captured, ok := r.Context().Value(nativeVideoRequestKey{}).(nativeVideoRequest)
				if !ok {
					t.Fatal("captureNativeVideoRequest stored no request")
				}
				seen = true
				gotID, gotOK = mediaRequestSessionID(captured.path, captured.streamToken, streamInsightsTestSecret)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.target, nil))
			if !seen {
				t.Fatal("handler did not run")
			}
			if gotID != tc.want || gotOK != tc.wantOK {
				t.Fatalf("session = (%q, %v), want (%q, %v)", gotID, gotOK, tc.want, tc.wantOK)
			}
		})
	}
}

func TestSessionIDFromStreamToken(t *testing.T) {
	if id, ok := sessionIDFromStreamToken(mintStreamInsightsToken(t, "sess-9", "direct"), streamInsightsTestSecret); id != "sess-9" || !ok {
		t.Fatalf("playback token = (%q, %v), want (sess-9, true)", id, ok)
	}
	if id, ok := sessionIDFromStreamToken(mintStreamInsightsToken(t, "sess-9", "direct"), "another-secret-0123456789"); id != "" || ok {
		t.Fatalf("token under another secret = (%q, %v), want refused", id, ok)
	}
	if id, ok := sessionIDFromStreamToken("", streamInsightsTestSecret); id != "" || ok {
		t.Fatalf("empty token = (%q, %v), want refused", id, ok)
	}
}
