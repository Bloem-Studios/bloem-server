package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestInitialHeaderMediaHTTPAndSeek(t *testing.T) {
	for _, hls := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "video-HLS"}[hls], func(t *testing.T) {
			f := newInitialHTTPFixture(t)
			f.request.ClientFeatures = append(f.request.ClientFeatures, playback.FeatureHeaderAuthenticatedMediaV3, playback.FeatureAuthorizedMediaOriginsV3)
			if hls {
				ffmpeg, err := exec.LookPath("ffmpeg")
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "12", "-c:v", "mpeg4", "-c:a", "aac", f.file.FilePath)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("video: %v %s", err, out)
				}
				f.file.CodecVideo = "mpeg4"
				f.file.Duration = 12
				f.file.Resolution = "180p"
				f.file.VideoTracks = []models.VideoTrack{{Codec: "mpeg4", Width: 320, Height: 180, FrameRate: "24/1", BitDepth: 8, VideoRange: "SDR"}}
				dir := t.TempDir()
				f.handler.PlaybackConfig = func() config.PlaybackConfig {
					return config.PlaybackConfig{TranscodeEnabled: true, HWAccel: "none", FFmpegPath: ffmpeg, TranscodeDir: dir}
				}
				f.request.ClientPlaybackContext.Deliveries[playback.DeliveryClassHLSV3] = playback.DeliveryCapabilityV3{Enabled: true, SupportedOnDevice: true}
			}
			status, data := f.call(t, http.MethodPost, "/start", f.request)
			var started playback.DecisionResponseV3
			if status != 201 || json.Unmarshal(data, &started) != nil || started.PlaybackPlan == nil {
				t.Fatalf("start %d %s", status, data)
			}
			if !playback.HasFeatureV3(started.ServerFeatures, playback.FeatureHeaderAuthenticatedMediaV3) {
				t.Fatal("feature missing")
			}
			fetch := func(path, profile string, auth bool) (int, []byte) {
				t.Helper()
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if auth {
					req.Header.Set("Authorization", "Bearer fixture-access")
				}
				req.Header.Set("X-Profile-Id", profile)
				resp, err := f.server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				return resp.StatusCode, b
			}
			check := func(plan *playback.PlanV3) {
				t.Helper()
				if !strings.HasPrefix(plan.Stream.URL, "/api/v2/") || strings.Contains(plan.Stream.URL, "st=") || plan.Stream.Headers["X-Profile-Id"] != f.request.ProfileID {
					t.Fatalf("projection %+v", plan.Stream)
				}
				for _, bad := range []struct {
					p string
					a bool
				}{{f.request.ProfileID, false}, {"wrong", true}, {"", true}} {
					if code, _ := fetch(plan.Stream.URL, bad.p, bad.a); code == 200 {
						t.Fatal("wrong authority served")
					}
				}
				path := plan.Stream.URL
				for depth := 0; depth < 4; depth++ {
					code, body := fetch(path, f.request.ProfileID, true)
					if code != 200 || len(body) == 0 {
						t.Fatalf("media %d %s", code, body)
					}
					if !strings.Contains(string(body), "#EXTM3U") {
						return
					}
					found := false
					for line := range strings.SplitSeq(string(body), "\n") {
						line = strings.TrimSpace(line)
						if line != "" && !strings.HasPrefix(line, "#") {
							if strings.HasSuffix(line, ".ts") && !strings.Contains(line, fmt.Sprintf("seg_%05d.ts", int(plan.Timeline.PlayerStartSeconds/2))) {
								continue
							}
							base, _ := url.Parse(path)
							ref, err := url.Parse(line)
							if err != nil {
								t.Fatal(err)
							}
							path = base.ResolveReference(ref).String()
							if strings.Contains(path, "st=") {
								t.Fatal("playlist leaked credential")
							}
							found = true
							break
						}
					}
					if !found {
						t.Fatal("playlist has no media")
					}
				}
				t.Fatal("playlist recursion")
			}
			check(started.PlaybackPlan)
			req := initialReplanRequestV3(started, f.request, "header-seek", 3)
			next, err := f.handler.ReplanInitialPlayback(initialContextV3(t, f), initialCallerV3(f), started.SessionID, replanCommandV3(t, req))
			if err != nil {
				t.Fatal(err)
			}
			check(next.PlaybackPlan)
			replay, err := f.handler.ReplanInitialPlayback(initialContextV3(t, f), initialCallerV3(f), started.SessionID, replanCommandV3(t, req))
			if err != nil || replay.PlaybackPlan.Stream.URL != next.PlaybackPlan.Stream.URL {
				t.Fatalf("replay: %v", err)
			}
		})
	}
}

func TestInitialHeaderProjectionPreservesAuxiliaryPins(t *testing.T) {
	for _, proxy := range []bool{false, true} {
		for _, hls := range []bool{false, true} {
			plan := &playback.PlanV3{SessionID: "session", Delivery: playback.DeliveryOriginalHTTPV3, Stream: playback.StreamV3{URL: "https://proxy.example.test/prefix/stream/direct/old"}, Subtitle: playback.SubtitleDecisionV3{Inventory: []playback.SubtitleInventoryItemV3{{URL: "/stream/session/subtitles/1.ass?file_id=42&embedded_stream_index=2&st=old", FontBundleURL: "/stream/session/subtitles/1/fonts?file_id=42&embedded_stream_index=2&st=old"}}}}
			if hls {
				plan.Delivery = playback.DeliveryTranscodeHLSV3
			}
			session := &playback.Session{ID: "session", ProfileID: "profile"}
			if proxy {
				session.RoutingEgressNodeID = 2
			}
			mode := mediaAuthModeV3{headerAuth: true, proxyEgress: proxy}
			if err := projectInitialHeaderMediaV3(plan, session, mode); err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{plan.Stream.URL, plan.Subtitle.Inventory[0].URL, plan.Subtitle.Inventory[0].FontBundleURL} {
				u, err := url.Parse(raw)
				if err != nil || u.Query().Has("st") || strings.Contains(raw, "old") {
					t.Fatalf("credential: %s", raw)
				}
				if proxy && u.Host != "proxy.example.test" {
					t.Fatal("origin changed")
				}
			}
			q, _ := url.Parse(plan.Subtitle.Inventory[0].URL)
			if q.Query().Get("file_id") != "42" || q.Query().Get("embedded_stream_index") != "2" {
				t.Fatal("pins changed")
			}
			before, _ := json.Marshal(plan)
			if err := projectInitialHeaderMediaV3(plan, session, mode); err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(plan)
			if string(before) != string(after) {
				t.Fatal("successor projection drift")
			}
			if proxy {
				if err := projectInitialHeaderMediaV3(plan, session, mediaAuthModeV3{headerAuth: true}); err == nil {
					t.Fatal("unnegotiated origin accepted")
				}
			}
		}
	}
}
