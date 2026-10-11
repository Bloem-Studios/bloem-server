package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/redis/go-redis/v9"
)

func TestNativeVideoRemotePreparationAndRollback(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, _, _ := nativeVideoGrantFixture(t, client)
	service.WorkerGrants = NewNativeVideoWorkerGrantStore(client)
	file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
	if err != nil {
		t.Fatal(err)
	}
	var received transcodenode.TranscodeStartRequest
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/transcode/start" {
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			grant, err := service.WorkerGrants.Resolve(r.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: received.SessionID, InputPath: received.InputPath, Start: &received}, service)
			if err != nil || grant == nil {
				t.Errorf("POST arrived without exact grant: %v", err)
				w.WriteHeader(403)
				return
			}
			writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{SessionID: received.SessionID, Status: "started", HWAccel: received.HWAccel, AudioRecipeVersion: received.AudioRecipeVersion, CopyFMP4RecipeVersion: received.CopyFMP4RecipeVersion, ThrottleSeconds: received.ThrottleSeconds})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer node.Close()
	h := NewPlaybackHandler(service.Sessions, service.Files)
	h.NativeVideo, h.JWTSecret = service, "fixture-secret"
	result := remoteHLSResultV3()
	result.Plan.EffectiveMediaFileID = file.ID
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	transport, failure := h.prepareRemoteTransportV3(request, session, file, result, nodepool.Plan{TranscodeNode: &nodepool.Node{ID: 7, URL: node.URL}}, preparedTimelineV3{}, mediaAuthModeV3{})
	if failure != nil {
		t.Fatalf("prepare: %v", failure)
	}
	card := remoteTranscodeRecipeCardV3(session, file, node.URL, transport.transportID, received, transcodenode.TranscodeStartResponse{HWAccel: received.HWAccel}, "")
	card.RoutingWorkload, card.RoutingExecution, card.RoutingEgress, card.RoutingExecutionNodeID = string(transport.routingWorkload), string(transport.routingExecution), string(transport.routingEgress), 7
	card.RoutingNetworkProvider = new(string) // same empty provider as the request
	if _, err := service.WorkerGrants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: transport.transportID, InputPath: file.FilePath, Recipe: &card}, service); err != nil {
		transport.rollback()
		t.Fatalf("accepted GET rejected: %v", err)
	}
	transport.rollback()
	if _, err := service.WorkerGrants.Resolve(context.Background(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: transport.transportID, InputPath: file.FilePath, Recipe: &card}, service); err == nil {
		t.Fatal("rollback left source grant usable")
	}
}

func TestNativeVideoRemoteHeaderAuthRequiresRecipeBeforePublication(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "absent_store", false: "failed_write"}[missing], func(t *testing.T) {
			client := nativeVideoGrantRedis(t)
			service, session, _, _ := nativeVideoGrantFixture(t, client)
			service.WorkerGrants = NewNativeVideoWorkerGrantStore(client)
			file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
			if err != nil {
				t.Fatal(err)
			}
			var received transcodenode.TranscodeStartRequest
			node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/transcode/start" {
					if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{SessionID: received.SessionID, HWAccel: received.HWAccel, AudioRecipeVersion: received.AudioRecipeVersion, CopyFMP4RecipeVersion: received.CopyFMP4RecipeVersion, ThrottleSeconds: received.ThrottleSeconds})
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer node.Close()
			h := NewPlaybackHandler(service.Sessions, service.Files)
			h.NativeVideo, h.JWTSecret = service, "fixture-secret"
			if !missing {
				h.NodeRecipeStore = &recordingRecipeCardStoreV3{putErr: errors.New("recipe write failed")}
			}
			result := remoteHLSResultV3()
			result.Plan.EffectiveMediaFileID = file.ID
			transport, failure := h.prepareRemoteTransportV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, nodepool.Plan{TranscodeNode: &nodepool.Node{ID: 7, URL: node.URL}}, preparedTimelineV3{}, mediaAuthModeV3{headerAuth: true})
			if failure == nil {
				transport.rollback()
				t.Fatal("native header-auth route published without its serving recipe")
			}
			if _, err := service.WorkerGrants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: received.SessionID, InputPath: file.FilePath, Start: &received}, service); err == nil {
				t.Fatal("failed route publication left native worker grant usable")
			}
		})
	}
}

func TestNativeVideoRemoteStopKeepsSessionWhenRevocationFails(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, start, _ := nativeVideoGrantFixture(t, client)
	store := NewNativeVideoWorkerGrantStore(client)
	if err := store.Prepare(t.Context(), 7, service, session, start); err != nil {
		t.Fatal(err)
	}
	manager := service.Sessions.(*playback.SessionManager)
	if err := manager.SetTranscodeRoute(session.ID, playback.TranscodeRoute{NodeURL: "http://127.0.0.1:1", TransportID: start.SessionID}); err != nil {
		t.Fatal(err)
	}
	session, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	dead := redis.NewClient(client.Options())
	_ = dead.Close()
	service.WorkerGrants = NewNativeVideoWorkerGrantStore(dead)
	h := NewPlaybackHandler(manager, service.Files)
	h.NativeVideo = service
	if err := h.stopPlaybackSession(t.Context(), session, true); err == nil {
		t.Fatal("stop succeeded without durable native grant revocation")
	}
	if _, err := manager.GetSession(session.ID); err != nil {
		t.Fatalf("failed stop removed retryable session: %v", err)
	}
	service.WorkerGrants = store
	if err := h.stopPlaybackSession(t.Context(), session, true); err != nil {
		t.Fatalf("retry stop: %v", err)
	}
	if _, err := store.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Start: &start}, service); err == nil {
		t.Fatal("successful stop left grant usable")
	}
}

func TestNativeVideoLocalStopRequiresDurableDenial(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, _, _ := nativeVideoGrantFixture(t, client)
	manager := service.Sessions.(*playback.SessionManager)
	h := NewPlaybackHandler(manager, service.Files)
	h.NativeVideo = service
	dead := redis.NewClient(client.Options())
	_ = dead.Close()
	service.Bindings = NewNativeVideoBindingStore(dead)
	if err := h.stopPlaybackSession(t.Context(), session, true); err == nil {
		t.Fatal("local stop succeeded without durable native denial")
	}
	if _, err := manager.GetSession(session.ID); err != nil {
		t.Fatalf("failed local stop removed retryable session: %v", err)
	}
	service.Bindings = NewNativeVideoBindingStore(client)
	if err := h.stopPlaybackSession(t.Context(), session, true); err != nil {
		t.Fatalf("retry local stop: %v", err)
	}
	if value, err := client.Exists(t.Context(), playback.StreamDenyKey(session.ID)).Result(); err != nil || value != 1 {
		t.Fatalf("local stop omitted durable session denial: %v", err)
	}
}

func TestNativeVideoRemoteProxyPreparationRefusesBeforeStart(t *testing.T) {
	for _, headerAuth := range []bool{false, true} {
		t.Run(map[bool]string{false: "signed", true: "header"}[headerAuth], func(t *testing.T) {
			client := nativeVideoGrantRedis(t)
			service, session, _, _ := nativeVideoGrantFixture(t, client)
			service.WorkerGrants = NewNativeVideoWorkerGrantStore(client)
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
			key, _ := nativeVideoWorkerKey(7, transportGenerationV3(session.ID, result.Plan.PlanID))
			if n, err := client.Exists(t.Context(), key).Result(); err != nil || n != 0 {
				t.Fatal("proxy refusal created a worker grant")
			}
		})
	}
}

func TestNativeVideoRemoteProxyRoutingFallsBackToSameWorkerAPIRelay(t *testing.T) {
	for _, proxyOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "API_relay_fallback", true: "proxy_only_refused"}[proxyOnly], func(t *testing.T) {
			client := nativeVideoGrantRedis(t)
			service, session, _, _ := nativeVideoGrantFixture(t, client)
			service.WorkerGrants = NewNativeVideoWorkerGrantStore(client)
			file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
			if err != nil {
				t.Fatal(err)
			}
			starts := &atomic.Int32{}
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
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
			defer worker.Close()
			h := NewPlaybackHandler(service.Sessions, service.Files)
			h.NativeVideo, h.JWTSecret = service, "fixture-secret"
			planner := &progressiveFallbackPlannerV3{transcode: &nodepool.Node{ID: 7, URL: worker.URL}, proxy: &nodepool.Node{ID: 8, URL: "http://proxy.example"}}
			h.NodePlanner = planner
			result := remoteHLSResultV3()
			result.Plan.SessionID, result.Plan.EffectiveMediaFileID = session.ID, file.ID
			policy := config.DefaultPlaybackRoutingPolicy()
			policy.VideoTranscodeExecution = config.PlaybackExecutionWorkerOnly
			if proxyOnly {
				policy.VideoTranscodeEgress = config.PlaybackEgressProxyOnly
			}
			transport, failure := h.prepareTransportWithPolicyV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, mediaAuthModeV3{}, policy)
			if proxyOnly {
				if failure == nil {
					transport.rollback()
					t.Fatal("proxy-only native policy published an unsupported route")
				}
				if failure.reason != "route_capability_unavailable" || !failure.retryable || starts.Load() != 0 {
					t.Fatalf("proxy-only refusal: failure=%+v starts=%d", failure, starts.Load())
				}
				return
			}
			if failure != nil {
				t.Fatalf("legal API relay fallback failed: %v", failure)
			}
			defer transport.rollback()
			if starts.Load() != 1 || transport.routingEgress != noderouting.EgressAPI || transport.routingExecutorURL != worker.URL || !strings.HasPrefix(transport.url, "/playback/transcode/") {
				t.Fatalf("fallback failed to reuse worker via API relay: starts=%d egress=%s executor=%q url=%q", starts.Load(), transport.routingEgress, transport.routingExecutorURL, transport.url)
			}
			if len(planner.requests) < 2 || !planner.requests[0].NeedsProxy || planner.requests[len(planner.requests)-1].NeedsProxy {
				t.Fatalf("fallback did not retry topology: %+v", planner.requests)
			}
		})
	}
}

func TestNativeVideoRemoteOrdinaryPredecessorStillPublishes(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, _, _, _ := nativeVideoGrantFixture(t, client)
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
	h.NativeVideo, h.JWTSecret = service, "fixture-secret"
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
