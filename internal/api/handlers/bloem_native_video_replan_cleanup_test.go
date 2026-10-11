package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

type nativeVideoReplanFile struct {
	NativeVideoFile
	ctx context.Context
}

func (f *nativeVideoReplanFile) Context() context.Context { return f.ctx }

func TestNativeVideoIdentityReplanOwnsPreviousExecutionCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("blocking child uses a Unix shell")
	}
	for _, delivery := range []playback.DeliveryV3{playback.DeliveryOriginalHTTPV3, playback.DeliveryRemuxProgressiveV3} {
		for _, commit := range []bool{false, true} {
			name := string(delivery) + "/rollback"
			if commit {
				name = string(delivery) + "/commit"
			}
			t.Run(name, func(t *testing.T) {
				service, file, session, _, _ := nativeVideoFixture(t)
				service.OpenDelegated = func(ctx context.Context, current *models.MediaFile, _ NativeVideoBinding) (NativeVideoFile, error) {
					opened, err := service.Open(ctx, current)
					if err != nil {
						return nil, err
					}
					return &nativeVideoReplanFile{NativeVideoFile: opened, ctx: ctx}, nil
				}
				if err := service.Bind(t.Context(), session, file); err != nil {
					t.Fatal(err)
				}
				source, format, err := service.InputSource(t.Context(), session, file)
				if err != nil {
					t.Fatal(err)
				}
				bin := filepath.Join(t.TempDir(), "blocking-child")
				if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				previous, err := playback.StartTranscode(t.Context(), playback.TranscodeOpts{SessionID: session.ID, InputPath: file.FilePath, InputSource: source, InputFormat: format, FFmpegPath: bin, OutputDir: t.TempDir(), TargetCodecVideo: "h264", TargetCodecAudio: "aac", HWAccel: "none"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = previous.Close() })
				manager := service.Sessions.(*playback.SessionManager)
				if err := manager.SetTranscodeRoute(session.ID, playback.TranscodeRoute{TransportID: session.ID + "-previous"}); err != nil {
					t.Fatal(err)
				}
				if err := manager.SetNodeRoutingAssignment(session.ID, playback.NodeRoutingAssignment{Workload: string(noderouting.WorkloadVideoTranscode), Execution: string(noderouting.ExecutionTranscode), ExecutionNodeID: 7, ExecutionNodeURL: "http://stale-worker.example", Egress: string(noderouting.EgressProxy), EgressNodeID: 8, EgressNodeURL: "http://proxy.example"}); err != nil {
					t.Fatal(err)
				}
				session, err = manager.GetSession(session.ID)
				if err != nil {
					t.Fatal(err)
				}
				h := NewPlaybackHandler(manager, service.Files)
				h.NativeVideo, h.JWTSecret = service, "fixture-secret"
				if _, accepted := h.tm.SwapTranscodeSession(session.ID, previous); !accepted {
					t.Fatal("prior child was not installed")
				}
				planner := &recordingNodePlannerV3{}
				h.NodePlanner = planner
				h.ProxyGrantStore = &recordingRecipeCardStoreV3{cards: map[string]playback.RecipeCard{session.ID: {SessionID: session.ID}}}
				method := playback.PlayDirect
				if delivery == playback.DeliveryRemuxProgressiveV3 {
					method = playback.PlayRemux
				}
				result := playback.PlannerResultV3{PlayMethod: method, Plan: &playback.PlanV3{SessionID: session.ID, EffectiveMediaFileID: file.ID, Delivery: delivery}}
				transport, failure := h.prepareNativeVideoTransportV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, mediaAuthModeV3{}, config.DefaultPlaybackRoutingPolicy())
				if failure != nil {
					t.Fatal(failure)
				}
				u, err := url.Parse(transport.url)
				if err != nil {
					t.Fatal(err)
				}
				claims, err := streamtoken.Verify(u.Query().Get(streamTokenParam), h.JWTSecret)
				if err != nil {
					transport.rollback()
					t.Fatal(err)
				}
				if claims.TranscodeNode != "" || claims.TranscodeTransportID != "" || claims.RoutingExecutionNodeID != 0 || claims.RoutingEgressNodeID != 0 {
					t.Errorf("new local recipe retains old node identity: node=%q transport=%q executor=%d egress=%d", claims.TranscodeNode, claims.TranscodeTransportID, claims.RoutingExecutionNodeID, claims.RoutingEgressNodeID)
				}
				if commit {
					if err := transport.commit(); err != nil {
						t.Fatal(err)
					}
					if h.tm.GetTranscodeSession(session.ID) != nil || previous.IsRunning() {
						t.Errorf("commit left previous execution: local=%v running=%v", h.tm.GetTranscodeSession(session.ID) != nil, previous.IsRunning())
					}
					if _, found := h.ProxyGrantStore.Get(t.Context(), session.ID); found {
						t.Error("commit left previous proxy grant")
					}
					if len(planner.released) != 1 || planner.released[0] != session.ID {
						t.Errorf("previous reservation not released: %v", planner.released)
					}
				} else {
					transport.rollback()
					_, proxy := h.ProxyGrantStore.Get(t.Context(), session.ID)
					if h.tm.GetTranscodeSession(session.ID) != previous || !previous.IsRunning() || !proxy || len(planner.released) != 0 {
						t.Fatal("rollback destroyed previous execution or authority")
					}
				}
			})
		}
	}
}

// Refusing publication must leave the prior route and its accepted source grant
// usable, including when the caller supplied a stale integrated-route snapshot.
func TestNativeVideoRemoteIdentityReplanRequiresFreshStart(t *testing.T) {
	for _, delivery := range []playback.DeliveryV3{playback.DeliveryOriginalHTTPV3, playback.DeliveryRemuxProgressiveV3} {
		for _, variant := range []struct{ stale, filesystem bool }{{}, {stale: true}, {filesystem: true}, {stale: true, filesystem: true}} {
			stale := variant.stale
			name := string(delivery) + "/current"
			if stale {
				name = string(delivery) + "/stale_snapshot"
			}
			if variant.filesystem {
				name += "/filesystem_alternate"
			}
			t.Run(name, func(t *testing.T) {
				client := nativeVideoGrantRedis(t)
				service, session, start, card := nativeVideoGrantFixture(t, client)
				service.WorkerGrants = NewNativeVideoWorkerGrantStore(client)
				if err := service.WorkerGrants.Prepare(t.Context(), 7, service, session, start); err != nil {
					t.Fatal(err)
				}
				if err := service.WorkerGrants.Accept(t.Context(), 7, start.SessionID, card); err != nil {
					t.Fatal(err)
				}
				workerRequests := &atomic.Int32{}
				worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					workerRequests.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				defer worker.Close()
				manager := service.Sessions.(*playback.SessionManager)
				if err := manager.SetTranscodeRoute(session.ID, playback.TranscodeRoute{NodeURL: worker.URL, TransportID: start.SessionID}); err != nil {
					t.Fatal(err)
				}
				if !stale {
					var err error
					session, err = manager.GetSession(session.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
				file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
				if err != nil {
					t.Fatal(err)
				}
				if variant.filesystem {
					alternate := *file
					alternate.ID++
					alternate.FilePath = "/fixture/alternate.mp4"
					service.Files = mapPlaybackFileResolver{files: map[int]*models.MediaFile{file.ID: file, alternate.ID: &alternate}}
					file = &alternate
				}
				h := NewPlaybackHandler(manager, service.Files)
				h.NativeVideo, h.JWTSecret = service, "fixture-secret"
				planner := &recordingNodePlannerV3{}
				h.NodePlanner = planner
				recipes := &recordingRecipeCardStoreV3{cards: map[string]playback.RecipeCard{start.SessionID: card}}
				proxy := &recordingRecipeCardStoreV3{cards: map[string]playback.RecipeCard{session.ID: card}}
				h.NodeRecipeStore, h.ProxyGrantStore = recipes, proxy
				method := playback.PlayDirect
				if delivery == playback.DeliveryRemuxProgressiveV3 {
					method = playback.PlayRemux
				}
				result := playback.PlannerResultV3{PlayMethod: method, Plan: &playback.PlanV3{SessionID: session.ID, EffectiveMediaFileID: file.ID, Delivery: delivery}}
				request := httptest.NewRequest(http.MethodPost, "/", nil)
				var transport preparedTransportV3
				var failure *transportErrorV3
				if variant.filesystem {
					transport, failure = h.prepareIdentityTransportV3(request, session, file, result, preparedTimelineV3{}, mediaAuthModeV3{}, config.DefaultPlaybackRoutingPolicy(), nil)
				} else {
					transport, failure = h.prepareNativeVideoTransportV3(request, session, file, result, mediaAuthModeV3{}, config.DefaultPlaybackRoutingPolicy())
				}
				if failure == nil {
					transport.rollback()
					t.Fatal("remote identity replan published a URL instead of requiring a fresh start")
				}
				if failure.reason != "route_capability_unavailable" || !failure.retryable || !strings.Contains(failure.message, "fresh playback start") || transport.url != "" || transport.transportID != "" {
					t.Fatalf("incorrect fresh-start refusal: failure=%+v transport=%+v", failure, transport)
				}
				live, err := manager.GetSession(session.ID)
				if err != nil || live.TranscodeNodeURL != worker.URL || live.TranscodeTransportID != start.SessionID {
					t.Fatalf("refusal changed old session: session=%+v error=%v", live, err)
				}
				if workerRequests.Load() != 0 || len(planner.released) != 0 || len(recipes.ops) != 0 || len(proxy.ops) != 0 {
					t.Fatal("refusal touched prior worker, reservation, recipe, or proxy authority")
				}
				grantRequest := transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}
				if _, err := service.WorkerGrants.Resolve(t.Context(), grantRequest, service); err != nil {
					t.Fatalf("refusal invalidated prior accepted worker grant: %v", err)
				}
			})
		}
	}
}

func TestNativeVideoRemoteHLSReplanRequiresFreshStart(t *testing.T) {
	for _, route := range []string{"local", "remote", "dispatch"} {
		for _, variant := range []struct{ stale, filesystem bool }{{}, {stale: true}, {filesystem: true}, {stale: true, filesystem: true}} {
			stale := variant.stale
			name := route + "/current"
			if stale {
				name = route + "/stale_snapshot"
			}
			if variant.filesystem {
				name += "/filesystem_alternate"
			}
			t.Run(name, func(t *testing.T) {
				client := nativeVideoGrantRedis(t)
				service, session, start, card := nativeVideoGrantFixture(t, client)
				service.WorkerGrants = NewNativeVideoWorkerGrantStore(client)
				if err := service.WorkerGrants.Prepare(t.Context(), 7, service, session, start); err != nil {
					t.Fatal(err)
				}
				if err := service.WorkerGrants.Accept(t.Context(), 7, start.SessionID, card); err != nil {
					t.Fatal(err)
				}
				requests := &atomic.Int32{}
				worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					var req transcodenode.TranscodeStartRequest
					if r.Method == http.MethodPost {
						_ = json.NewDecoder(r.Body).Decode(&req)
						writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{SessionID: req.SessionID, Status: "started", HWAccel: req.HWAccel, AudioRecipeVersion: req.AudioRecipeVersion, CopyFMP4RecipeVersion: req.CopyFMP4RecipeVersion, ThrottleSeconds: req.ThrottleSeconds})
						return
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				defer worker.Close()
				manager := service.Sessions.(*playback.SessionManager)
				if err := manager.SetTranscodeRoute(session.ID, playback.TranscodeRoute{NodeURL: worker.URL, TransportID: start.SessionID}); err != nil {
					t.Fatal(err)
				}
				if !stale {
					var err error
					session, err = manager.GetSession(session.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
				file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
				if err != nil {
					t.Fatal(err)
				}
				target := file
				if variant.filesystem {
					alternate := *file
					alternate.ID++
					alternate.FilePath = "/fixture/alternate.mp4"
					target = &alternate
					service.Files = mapPlaybackFileResolver{files: map[int]*models.MediaFile{file.ID: file, target.ID: target}}
				}
				h := NewPlaybackHandler(manager, service.Files)
				h.NativeVideo, h.JWTSecret = service, "fixture-secret"
				h.PlaybackConfig = func() config.PlaybackConfig {
					return config.PlaybackConfig{TranscodeDir: t.TempDir(), FFmpegPath: "/missing-native-refusal-sentinel", HWAccel: "none"}
				}
				planner := &recordingNodePlannerV3{}
				h.NodePlanner = planner
				recipes := &recordingRecipeCardStoreV3{cards: map[string]playback.RecipeCard{start.SessionID: card}}
				proxy := &recordingRecipeCardStoreV3{cards: map[string]playback.RecipeCard{session.ID: card}}
				h.NodeRecipeStore, h.ProxyGrantStore = recipes, proxy
				opens := &atomic.Int32{}
				originalOpen := service.Open
				service.Open = func(ctx context.Context, f *models.MediaFile) (NativeVideoFile, error) {
					opens.Add(1)
					return originalOpen(ctx, f)
				}
				result := remoteHLSResultV3()
				result.Plan.SessionID, result.Plan.EffectiveMediaFileID = session.ID, target.ID
				request := httptest.NewRequest(http.MethodPost, "/", nil)
				var transport preparedTransportV3
				var failure *transportErrorV3
				switch route {
				case "local":
					transport, failure = h.prepareLocalTransportV3(request, session, target, result, preparedTimelineV3{}, mediaAuthModeV3{})
				case "remote":
					transport, failure = h.prepareRemoteTransportV3(request, session, target, result, nodepool.Plan{TranscodeNode: &nodepool.Node{ID: 7, URL: worker.URL}}, preparedTimelineV3{}, mediaAuthModeV3{})
				case "dispatch":
					transport, failure = h.prepareTransportWithPolicyV3(request, session, target, result, mediaAuthModeV3{}, config.DefaultPlaybackRoutingPolicy())
				}
				if failure == nil {
					transport.rollback()
					t.Fatal("HLS replacement published new authority instead of requiring a fresh start")
				}
				if failure.reason != "route_capability_unavailable" || !failure.retryable || !strings.Contains(failure.message, "fresh playback start") || transport.url != "" || transport.transportID != "" {
					t.Fatalf("incorrect fresh-start refusal: %+v", failure)
				}
				live, err := manager.GetSession(session.ID)
				if err != nil || live.TranscodeNodeURL != worker.URL || live.TranscodeTransportID != start.SessionID {
					t.Fatal("HLS refusal changed the live predecessor")
				}
				if requests.Load() != 0 || opens.Load() != 0 || len(planner.released) != 0 || len(recipes.ops) != 0 || len(proxy.ops) != 0 {
					t.Fatalf("HLS refusal touched execution/source/authority: requests=%d opens=%d", requests.Load(), opens.Load())
				}
				if _, err := service.WorkerGrants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}, service); err != nil {
					t.Fatalf("HLS refusal revoked old accepted grant: %v", err)
				}
			})
		}
	}
}
