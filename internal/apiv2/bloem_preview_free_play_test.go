package apiv2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// gateFreePlaySettings answers that the guest session holds a live grant.
type gateFreePlaySettings struct {
	gateGuestSettings
	live bool
}

func (g gateFreePlaySettings) LiveFreePlay(context.Context, string) (string, bool, error) {
	return "GrantGrantGrantGrantGr", g.live, nil
}

type fakeFreePlay struct {
	busy      bool
	bound     map[string]string
	stopped   []string
	delivered []string
}

func (f *fakeFreePlay) Busy(context.Context, string) (bool, error) { return f.busy, nil }
func (f *fakeFreePlay) BindPlayback(_ context.Context, playback, grant string) error {
	if f.bound == nil {
		f.bound = map[string]string{}
	}
	f.bound[playback] = grant
	return nil
}
func (f *fakeFreePlay) StopPlayback(playback string) { f.stopped = append(f.stopped, playback) }
func (f *fakeFreePlay) Deliver(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	f.delivered = append(f.delivered, r.URL.Path)
	return r, true
}

type freePlayGateCall struct {
	operation, path, body string
	live                  bool
	handler               http.HandlerFunc
}

func serveFreePlayGate(t *testing.T, svc *fakeFreePlay, call freePlayGateCall) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, call.path, strings.NewReader(call.body))
	r.Host = "browse.example.test"
	r.Header.Set("Authorization", "Bearer guest")
	sessions := previewGateSessions{preview: true}
	am := apimw.NewAuthMiddleware(fakeTokens{map[string]*auth.Claims{"guest": {UserID: 1, Role: "user", SessionID: "s1", TokenType: auth.TokenTypeAccess, AuthMethod: auth.AuthMethodPreviewGuest}}}, sessions, nil, nil)
	am.SetPreviewPrincipals(sessions)
	am.SetPreviewRouteGuard(func(*http.Request) bool { return true })
	am.SetPreviewGuests(gateFreePlaySettings{live: call.live}, nil)
	handler := call.handler
	if handler == nil {
		handler = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	}
	w := httptest.NewRecorder()
	am.RequireAuth(previewGateWith(svc, call.operation)(handler)).ServeHTTP(w, r)
	return w
}

const freePlayStartBody = `{"file_id":"f1","progress_persistence":"server","client_playback_context":{"deliveries":{
	"original_http":{"enabled":true,"supported_on_device":true},
	"progressive":{"enabled":true,"supported_on_device":true},
	"hls":{"enabled":true,"supported_on_device":true}}}}`

// A free-play start may only become HLS: the gate turns the direct and
// progressive deliveries off and progress stays on the device; the HLS
// session that comes back is bound to the grant, so its stream-token media
// requests find what to charge.
func TestFreePlayStartBecomesHLSAndIsBoundToTheGrant(t *testing.T) {
	svc := &fakeFreePlay{}
	var forwarded map[string]any
	w := serveFreePlayGate(t, svc, freePlayGateCall{operation: "startPlayback", path: "/api/v2/playback/start", body: freePlayStartBody, live: true,
		handler: func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &forwarded); err != nil {
				t.Errorf("forwarded body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"outcome":"playable","session_id":"p1","playback_plan":{"stream":{"url":"/api/v2/playback/transcode/p1/master.m3u8?st=x","container":"hls"}}}`))
		}})
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"session_id":"p1"`) {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	deliveries := forwarded["client_playback_context"].(map[string]any)["deliveries"].(map[string]any)
	for class, want := range map[string]bool{"original_http": false, "progressive": false, "hls": true} {
		if got := deliveries[class].(map[string]any)["enabled"]; got != want {
			t.Errorf("%s enabled = %v, want %v", class, got, want)
		}
	}
	if forwarded["progress_persistence"] != "client" {
		t.Errorf("progress_persistence = %v", forwarded["progress_persistence"])
	}
	// Client persistence needs a start position; a start without one begins
	// at the top.
	if forwarded["start_position"] != float64(0) {
		t.Errorf("start_position = %v, want 0", forwarded["start_position"])
	}
	if svc.bound["p1"] != "GrantGrantGrantGrantGr" || len(svc.stopped) != 0 {
		t.Fatalf("bound=%v stopped=%v", svc.bound, svc.stopped)
	}
}

// Should a plan come back that is not HLS, the session is stopped and the
// start refused: free play never hands out the file itself.
func TestFreePlayStartRefusesADirectPlan(t *testing.T) {
	svc := &fakeFreePlay{}
	w := serveFreePlayGate(t, svc, freePlayGateCall{operation: "startPlayback", path: "/api/v2/playback/start", body: freePlayStartBody, live: true,
		handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"outcome":"playable","session_id":"p2","playback_plan":{"stream":{"url":"/api/v2/stream/p2?st=x","container":"mp4"}}}`))
		}})
	if w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != apimw.CodePreviewOnly {
		t.Fatalf("direct plan: %d %s", w.Code, w.Header().Get("X-Bloem-Denial"))
	}
	if len(svc.stopped) != 1 || svc.stopped[0] != "p2" || len(svc.bound) != 0 {
		t.Fatalf("stopped=%v bound=%v", svc.stopped, svc.bound)
	}
}

// At capacity the start is refused as busy before any playback is planned.
func TestFreePlayStartIsRefusedWhenBusy(t *testing.T) {
	svc := &fakeFreePlay{busy: true}
	planned := false
	w := serveFreePlayGate(t, svc, freePlayGateCall{operation: "startPlayback", path: "/api/v2/playback/start", body: freePlayStartBody, live: true,
		handler: func(w http.ResponseWriter, _ *http.Request) { planned = true }})
	if w.Code != http.StatusTooManyRequests || w.Header().Get("X-Bloem-Denial") != apimw.CodeFreePlayBusy || planned {
		t.Fatalf("busy: %d %s planned=%v", w.Code, w.Header().Get("X-Bloem-Denial"), planned)
	}
}

// Only the free-play operations open, and only with a live grant; bearer
// media requests go through the meter.
func TestFreePlayGateOpensOnlyPlaybackForALiveGrant(t *testing.T) {
	for _, tc := range []struct {
		operation, path string
		live            bool
		want            int
	}{
		{"getPlaybackSegment", "/api/v2/playback/transcode/p1/segment/s1.m4s", true, http.StatusNoContent},
		{"getPlaybackManifest", "/api/v2/playback/transcode/p1/master.m3u8", true, http.StatusNoContent},
		{"stopPlayback", "/api/v2/playback/p1", true, http.StatusNoContent},
		{"updatePlaybackProgress", "/api/v2/playback/p1/progress", true, http.StatusNoContent},
		{"getPlaybackMedia", "/api/v2/stream/p1", true, http.StatusForbidden},
		{"replanPlayback", "/api/v2/playback/p1/replan", true, http.StatusForbidden},
		{"getDownloadManifest", "/api/v2/downloads/d1/manifest", true, http.StatusForbidden},
		{"getPlaybackSegment", "/api/v2/playback/transcode/p1/segment/s1.m4s", false, http.StatusForbidden},
		{"startPlayback", "/api/v2/playback/start", false, http.StatusForbidden},
	} {
		svc := &fakeFreePlay{}
		w := serveFreePlayGate(t, svc, freePlayGateCall{operation: tc.operation, path: tc.path, live: tc.live})
		if w.Code != tc.want {
			t.Errorf("%s live=%v: %d, want %d", tc.operation, tc.live, w.Code, tc.want)
		}
		media := tc.operation == "getPlaybackSegment" || tc.operation == "getPlaybackManifest"
		if media && tc.live && len(svc.delivered) != 1 {
			t.Errorf("%s did not go through the meter", tc.operation)
		}
	}
}
