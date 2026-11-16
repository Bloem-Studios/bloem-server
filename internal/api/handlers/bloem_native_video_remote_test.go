package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodeauth/nodeauthtest"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

func TestNativeVideoLocalStopRequiresDurableDenial(t *testing.T) {
	client := nativeVideoRedis(t)
	service, session, _, _ := nativeVideoBoundFixture(t, client)
	manager := service.Sessions.(*playback.SessionManager)
	h := NewPlaybackHandler(manager, service.Files)
	h.NativeVideo = service
	dead := redis.NewClient(client.Options())
	_ = dead.Close()
	service.Bindings = NewNativeVideoBindingStore(dead)
	if err := h.stopPlaybackSession(t.Context(), session, PlaybackEndStopped); err == nil {
		t.Fatal("local stop succeeded without durable native denial")
	}
	if _, err := manager.GetSession(session.ID); err != nil {
		t.Fatalf("failed local stop removed retryable session: %v", err)
	}
	service.Bindings = NewNativeVideoBindingStore(client)
	if err := h.stopPlaybackSession(t.Context(), session, PlaybackEndStopped); err != nil {
		t.Fatalf("retry local stop: %v", err)
	}
	if value, err := client.Exists(t.Context(), playback.StreamDenyKey(session.ID)).Result(); err != nil || value != 1 {
		t.Fatalf("local stop omitted durable session denial: %v", err)
	}
}

func TestNativeVideoRemoteProxyPreparationRefusesBeforeStart(t *testing.T) {
	for _, headerAuth := range []bool{false, true} {
		t.Run(map[bool]string{false: "signed", true: "header"}[headerAuth], func(t *testing.T) {
			client := nativeVideoRedis(t)
			service, session, _, _ := nativeVideoBoundFixture(t, client)
			file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
			if err != nil {
				t.Fatal(err)
			}
			requests := &atomic.Int32{}
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method == http.MethodPost {
					var req transcodenode.TranscodeStartRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{SessionID: req.SessionID, Status: "started", HWAccel: req.HWAccel, AudioRecipeVersion: req.AudioRecipeVersion, CopyFMP4RecipeVersion: req.CopyFMP4RecipeVersion, ThrottleSeconds: req.ThrottleSeconds})
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer worker.Close()
			h := NewPlaybackHandler(service.Sessions, service.Files)
			h.NativeVideo, h.JWTSecret = service, "fixture-secret"
			recipes, proxy := &recordingRecipeCardStoreV3{}, &recordingRecipeCardStoreV3{}
			h.NodeRecipeStore, h.ProxyGrantStore = recipes, proxy
			result := remoteHLSResultV3()
			result.Plan.EffectiveMediaFileID = file.ID
			plan := nodepool.Plan{TranscodeNode: &nodepool.Node{ID: 7, URL: worker.URL}, ProxyNode: &nodepool.Node{ID: 8, URL: "http://proxy.example"}}
			transport, failure := h.prepareRemoteTransportV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, plan, preparedTimelineV3{}, mediaAuthModeV3{headerAuth: headerAuth, proxyEgress: headerAuth})
			if failure == nil {
				transport.rollback()
				t.Fatal("native HLS published an unsupported proxy route")
			}
			if failure.reason != "route_capability_unavailable" || !failure.retryable || transport.url != "" || requests.Load() != 0 || len(recipes.ops) != 0 || len(proxy.ops) != 0 {
				t.Fatalf("proxy refusal ran media or published authority: failure=%+v requests=%d", failure, requests.Load())
			}
		})
	}
}

// nativeExecutionWorker is a transcode node that would have passed Bloem's
// former native readiness probe. Silo's thin nodes have no database or
// cipher and read sources only through core grants, so no native start may
// reach one, readiness answer or not. starts counts POST /transcode/start.
func nativeExecutionWorker(t *testing.T, starts *atomic.Int32) *httptest.Server {
	t.Helper()
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/bloem/native-execution":
			writeJSON(w, http.StatusOK, map[string]any{"native_execution": true, "node_id": 7})
		case r.Method == http.MethodGet && r.URL.Path == "/hw-capabilities":
			writeJSON(w, http.StatusOK, playback.HWAccelInfo{Transformations: remoteHLSPlanV3().Transformations})
		case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
			starts.Add(1)
			var req transcodenode.TranscodeStartRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{SessionID: req.SessionID, Status: "started", HWAccel: req.HWAccel, AudioRecipeVersion: req.AudioRecipeVersion, CopyFMP4RecipeVersion: req.CopyFMP4RecipeVersion, ThrottleSeconds: req.ThrottleSeconds})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(worker.Close)
	return worker
}

// A native HLS start is refused on every transcode node before a source
// grant is minted or a start is sent, so the route loop excludes the node.
func TestNativeVideoRemoteStartNeverReachesATranscodeNode(t *testing.T) {
	for _, headerAuth := range []bool{false, true} {
		t.Run(map[bool]string{false: "signed", true: "header"}[headerAuth], func(t *testing.T) {
			client := nativeVideoRedis(t)
			service, session, _, _ := nativeVideoBoundFixture(t, client)
			file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
			if err != nil {
				t.Fatal(err)
			}
			starts := &atomic.Int32{}
			worker := nativeExecutionWorker(t, starts)
			h := NewPlaybackHandler(service.Sessions, service.Files)
			h.NativeVideo, h.JWTSecret, h.NodeKeys = service, "fixture-secret", nodeauthtest.SameKey("fixture-node-key")
			recipes := &recordingRecipeCardStoreV3{}
			h.NodeRecipeStore = recipes
			result := remoteHLSResultV3()
			result.Plan.EffectiveMediaFileID = file.ID
			transport, failure := h.prepareRemoteTransportV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, nodepool.Plan{TranscodeNode: &nodepool.Node{ID: 7, URL: worker.URL}}, preparedTimelineV3{}, mediaAuthModeV3{headerAuth: headerAuth})
			if failure == nil {
				transport.rollback()
				t.Fatal("a native HLS start was sent to a transcode node")
			}
			if failure.reason != routeCapabilityUnavailableReasonV3 || !failure.retryable || !failure.executorIneligible || !errors.Is(failure.cause, errNativeVideoAPIOnly) {
				t.Fatalf("refusal = %+v, want an ineligible executor", failure)
			}
			if starts.Load() != 0 || len(recipes.ops) != 0 {
				t.Fatalf("refusal ran media or stored a recipe: starts=%d recipes=%d", starts.Load(), len(recipes.ops))
			}
		})
	}
}

// A policy that runs video transcodes only on nodes leaves a native input
// with no route, whatever its egress, instead of starting it on a node.
func TestNativeVideoNodeOnlyPolicyHasNoRoute(t *testing.T) {
	for _, proxyOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "API_relay", true: "proxy_only"}[proxyOnly], func(t *testing.T) {
			client := nativeVideoRedis(t)
			service, session, _, _ := nativeVideoBoundFixture(t, client)
			file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
			if err != nil {
				t.Fatal(err)
			}
			starts := &atomic.Int32{}
			worker := nativeExecutionWorker(t, starts)
			h := NewPlaybackHandler(service.Sessions, service.Files)
			h.NativeVideo, h.JWTSecret, h.NodeKeys = service, "fixture-secret", nodeauthtest.SameKey("fixture-node-key")
			h.NodePlanner = &progressiveFallbackPlannerV3{transcode: &nodepool.Node{ID: 7, URL: worker.URL}, proxy: &nodepool.Node{ID: 8, URL: "http://proxy.example"}}
			result := remoteHLSResultV3()
			result.Plan.SessionID, result.Plan.EffectiveMediaFileID = session.ID, file.ID
			policy := config.DefaultPlaybackRoutingPolicy()
			policy.VideoTranscodeExecution = config.PlaybackExecutionWorkerOnly
			if proxyOnly {
				policy.VideoTranscodeEgress = config.PlaybackEgressProxyOnly
			}
			transport, failure := h.prepareTransportWithPolicyV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, mediaAuthModeV3{}, policy)
			if failure == nil {
				transport.rollback()
				t.Fatal("a node-only policy published a native route")
			}
			if !failure.retryable || starts.Load() != 0 {
				t.Fatalf("refusal: failure=%+v starts=%d", failure, starts.Load())
			}
		})
	}
}

func TestNativeVideoRemoteOrdinaryPredecessorStillPublishes(t *testing.T) {
	client := nativeVideoRedis(t)
	service, _, _, _ := nativeVideoBoundFixture(t, client)
	file := &models.MediaFile{ID: 101, ContentID: "ordinary", MediaFolderID: 9, FilePath: "/fixture/ordinary.mp4", Container: "mp4", CodecVideo: "h264"}
	service.Files = mapPlaybackFileResolver{files: map[int]*models.MediaFile{file.ID: file}}
	manager := service.Sessions.(*playback.SessionManager)
	session, err := manager.StartSessionWithFilesContext(t.Context(), 7, "profile", file.ID, file.ID, playback.PlayTranscode, false)
	if err != nil {
		t.Fatal(err)
	}
	starts := &atomic.Int32{}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			starts.Add(1)
			var req transcodenode.TranscodeStartRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{SessionID: req.SessionID, Status: "started", HWAccel: req.HWAccel, AudioRecipeVersion: req.AudioRecipeVersion, CopyFMP4RecipeVersion: req.CopyFMP4RecipeVersion, ThrottleSeconds: req.ThrottleSeconds})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer worker.Close()
	if err := manager.SetTranscodeRoute(session.ID, playback.TranscodeRoute{NodeURL: worker.URL, TransportID: session.ID + "-previous"}); err != nil {
		t.Fatal(err)
	}
	session, err = manager.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := NewPlaybackHandler(manager, service.Files)
	h.NativeVideo, h.JWTSecret, h.NodeKeys = service, "fixture-secret", nodeauthtest.SameKey("fixture-node-key")
	result := remoteHLSResultV3()
	result.Plan.SessionID, result.Plan.EffectiveMediaFileID = session.ID, file.ID
	transport, failure := h.prepareRemoteTransportV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, nodepool.Plan{TranscodeNode: &nodepool.Node{ID: 7, URL: worker.URL}, ProxyNode: &nodepool.Node{ID: 8, URL: "http://proxy.example"}}, preparedTimelineV3{}, mediaAuthModeV3{})
	if failure != nil {
		t.Fatalf("ordinary predecessor was denied: %v", failure)
	}
	defer transport.rollback()
	if starts.Load() != 1 || transport.routingEgress != noderouting.EgressProxy || !strings.HasPrefix(transport.url, "http://proxy.example/") {
		t.Fatalf("ordinary proxy route changed: starts=%d egress=%s url=%q", starts.Load(), transport.routingEgress, transport.url)
	}
}
