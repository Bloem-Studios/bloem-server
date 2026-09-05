package jellycompat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type compatGrantResponse struct{ *httptest.ResponseRecorder }

func (w *compatGrantResponse) SetWriteDeadline(time.Time) error { return nil }
func (w *compatGrantResponse) FlushError() error                { return nil }

func TestCompatBoundHLSResponse(t *testing.T) {
	for _, scenario := range []string{"manifest", "master", "segment", "stripped", "metadata", "route", "owner", "expired", "revoked-metadata", "disconnect", "subtitle", "subtitle-stripped", "progress", "stop", "delete", "progress-stripped", "stop-stripped"} {
		t.Run(scenario, func(t *testing.T) {
			ns := &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
			clock, err := playback.NewRuntimeGrantClockV3()
			if err != nil {
				t.Fatal(err)
			}
			provider := func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
				a := playback.AttemptAuthorityV3{PlaybackAttemptID: "attempt", Incarnation: ns.Incarnation, Epoch: 1, OwnerID: uuid.NewString(), State: playback.AttemptActiveV3, LeaseExpiresAt: time.Now().Add(time.Hour)}
				req := playback.AttemptGrantRequestV3{Executor: executor, SessionID: "upstream", PlanID: "plan", TransportID: transport, NodeID: 1, Purpose: purpose, Duration: time.Minute}
				return playback.AcquireRuntimeGrantV3(ctx, func(_ context.Context, a playback.AttemptAuthorityV3, r playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
					now := time.Now()
					return playback.AttemptGrantV3{Authority: a, Request: r, IssuedAt: now, NotAfter: now.Add(time.Minute)}, nil
				}, clock, playback.RuntimeGrantPolicyV3{MaxDuration: time.Minute, SafetyMargin: time.Second, RenewBefore: 2 * time.Second, PollInterval: 100 * time.Millisecond}, a, req)
			}
			dir, _ := ns.OutputDir(t.TempDir())
			bin, err := exec.LookPath("true")
			if err != nil {
				t.Fatal(err)
			}
			source := PlaybackMediaSource{FileID: 1}
			opts := playback.TranscodeOpts{Executor: ns, ExecuteGrants: provider, SessionID: "upstream", OutputDir: dir, InputPath: "/fixture/movie.mkv", FFmpegPath: bin, HWAccel: playback.HWAccelNone, TargetCodecVideo: "h264", SegmentDuration: 6, AudioTrackIndex: compatAudioTrackIndexOrDefault(source)}
			live, err := playback.StartTranscode(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = live.Close() })
			for name, data := range map[string]string{"stream.m3u8": "#EXTM3U\n#EXTINF:6,\nseg_00000.ts\n#EXTINF:6,\nseg_00001.ts\n#EXTINF:6,\nseg_00002.ts\n#EXT-X-ENDLIST\n", "seg_00000.ts": "segment bytes", "seg_00001.ts": "segment bytes", "seg_00002.ts": "segment bytes"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			card := playback.NewRecipeCard(7, "profile", 1, "", opts)
			card.RoutingExecution = "api"
			card.RoutingEgress = "api"
			card.RoutingWorkload = "video_transcode"
			tm := playback.NewTranscodeManager()
			tm.RegisterTranscodeSession("upstream", live)
			tm.ExecuteGrants = provider
			var serveGrant *playback.RuntimeGrantV3
			if scenario == "revoked-metadata" {
				tm.ExecuteGrants = func(ctx context.Context, tr string, n playback.ExecutorNamespaceV3, p playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
					g, e := provider(ctx, tr, n, p)
					serveGrant = g
					return g, e
				}
			}
			ps := PlaybackSession{ID: "play", CompatToken: "token", RouteItemID: "item", UpstreamSessionID: "upstream", UpstreamPlayMethod: "transcode", Recipe: &card, MediaSources: []PlaybackMediaSource{source}, RoutingAssignment: &playback.NodeRoutingAssignment{Workload: "video_transcode", Execution: "api", Egress: "api"}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tm.ResolveExecutorRecipe = func(ctx context.Context, _ string, _ playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
				copy := card
				if scenario == "revoked-metadata" {
					serveGrant.Close()
				}
				if scenario == "route" {
					copy.RoutingEgress = "proxy"
				}
				if scenario == "owner" {
					copy.UserID = 8
				}
				if scenario == "metadata" {
					copy.MediaFileID = 2
				}
				if scenario == "disconnect" {
					cancel()
					<-ctx.Done()
				}
				return &copy, nil
			}
			if scenario == "stripped" || strings.HasSuffix(scenario, "-stripped") {
				copy := card
				copy.Executor = nil
				ps.Recipe = &copy
			}
			if scenario == "expired" {
				tm.ExecuteGrants = func(ctx context.Context, tr string, n playback.ExecutorNamespaceV3, p playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
					g, e := provider(ctx, tr, n, p)
					if g != nil {
						g.Close()
					}
					return g, e
				}
			}
			store := NewPlaybackSessionStore(0, nil)
			store.Put(ps)
			h := &PlaybackHandler{tm: tm, playbackStore: store}
			route := chi.NewRouteContext()
			route.URLParams.Add("id", "item")
			route.URLParams.Add("playlistId", "play")
			route.URLParams.Add("segmentId", "seg_00000")
			route.URLParams.Add("segmentContainer", "ts")
			ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
			ctx = context.WithValue(ctx, compatSessionKey, &Session{Token: "token", StreamAppUserID: 7, ProfileID: "profile"})
			request := httptest.NewRequest(http.MethodGet, "/Videos/item/hls/play/stream.m3u8", nil).WithContext(ctx)
			w := &compatGrantResponse{httptest.NewRecorder()}
			switch scenario {
			case "progress", "progress-stripped", "stop", "stop-stripped", "delete":
				request.URL.RawQuery = "PlaySessionId=play"
				request.Body = io.NopCloser(strings.NewReader(`{"PlaySessionId":"play","PositionTicks":100000000,"AudioStreamIndex":2}`))
				if scenario == "delete" {
					h.HandleDeleteActiveEncodings(w, request)
				} else {
					h.handlePlaybackReport(w, request, strings.HasPrefix(scenario, "stop"))
				}
				if w.Code != http.StatusServiceUnavailable {
					t.Fatalf("lifecycle accepted: %d %s", w.Code, w.Body.String())
				}
				if got, ok := store.Get("play"); !ok || got.Terminal || tm.GetTranscodeSession("upstream") != live {
					t.Fatal("refusal mutated playback state")
				}
			case "subtitle", "subtitle-stripped":
				request.URL.RawQuery = "PlaySessionId=play"
				h.HandleSubtitleStream(w, request)
				if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "Executor-bound subtitle") {
					t.Fatalf("subtitle escaped pre-lookup guard: %d %s", w.Code, w.Body.String())
				}
			case "master":
				request.URL.RawQuery = "PlaySessionId=play"
				h.HandleMasterManifest(w, request)
			case "segment":
				h.HandleHLSSegment(w, request)
			default:
				h.HandleHLSManifest(w, request)
			}
			body := w.Body.String()
			if scenario == "manifest" || scenario == "master" {
				if !strings.Contains(body, "#EXTM3U") {
					t.Fatalf("manifest: %d %s", w.Code, body)
				}
			} else if scenario == "segment" {
				if body != "segment bytes" {
					t.Fatalf("segment: %d %s", w.Code, body)
				}
			} else if strings.Contains(body, "#EXTM3U") || strings.Contains(body, "segment bytes") {
				t.Fatalf("forbidden delivery: %s", body)
			}
		})
	}
}
