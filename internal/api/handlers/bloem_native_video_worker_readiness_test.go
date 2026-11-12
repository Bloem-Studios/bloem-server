package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// answerNativeExecutionReadiness is a fake worker's answer to the readiness
// probe of a worker that runs native execution with approved storage.
func answerNativeExecutionReadiness(w http.ResponseWriter, r *http.Request, nodeID int) bool {
	if r.Method != http.MethodGet || r.URL.Path != transcodenode.NativeExecutionReadinessPath {
		return false
	}
	writeJSON(w, http.StatusOK, transcodenode.NativeExecutionReadiness{NativeExecution: true, NodeID: nodeID})
	return true
}

// A native HLS start goes to a worker only when the worker reports that it
// can read native storage. Other workers are skipped before any grant or
// start, so the route can fall back to the API server when policy allows.
func TestNativeVideoRemoteSkipsWorkerWithoutNativeStorage(t *testing.T) {
	cases := []struct {
		name   string
		answer func(http.ResponseWriter, *http.Request) bool
	}{
		{name: "no_probe_route", answer: func(http.ResponseWriter, *http.Request) bool { return false }},
		{name: "probe_says_not_ready", answer: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != transcodenode.NativeExecutionReadinessPath {
				return false
			}
			writeJSON(w, http.StatusOK, transcodenode.NativeExecutionReadiness{NativeExecution: false, NodeID: 7})
			return true
		}},
		{name: "probe_names_another_worker", answer: func(w http.ResponseWriter, r *http.Request) bool {
			return answerNativeExecutionReadiness(w, r, 8)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := nativeVideoGrantRedis(t)
			service, session, _, _ := nativeVideoGrantFixture(t, client)
			service.WorkerGrants = NewNativeVideoWorkerGrantStore(client)
			file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
			if err != nil {
				t.Fatal(err)
			}
			starts, probes := &atomic.Int32{}, &atomic.Int32{}
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == transcodenode.NativeExecutionReadinessPath {
					probes.Add(1)
				}
				if tc.answer(w, r) {
					return
				}
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/hw-capabilities":
					writeJSON(w, http.StatusOK, playback.HWAccelInfo{Transformations: remoteHLSPlanV3().Transformations})
				case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
					starts.Add(1)
					var req transcodenode.TranscodeStartRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{SessionID: req.SessionID, Status: "started", HWAccel: req.HWAccel, AudioRecipeVersion: req.AudioRecipeVersion, CopyFMP4RecipeVersion: req.CopyFMP4RecipeVersion, ThrottleSeconds: req.ThrottleSeconds})
				default:
					w.WriteHeader(http.StatusNotFound)
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
			policy.VideoTranscodeEgress = config.PlaybackEgressAPIOnly
			transport, failure := h.prepareTransportWithPolicyV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, mediaAuthModeV3{}, policy)
			if failure == nil {
				transport.rollback()
				t.Fatal("native HLS started on a worker that cannot read native storage")
			}
			if starts.Load() != 0 || probes.Load() == 0 {
				t.Fatalf("starts=%d probes=%d; want the probe and no start", starts.Load(), probes.Load())
			}
			key, _ := nativeVideoWorkerKey(7, transportGenerationV3(session.ID, result.Plan.PlanID))
			if n, err := client.Exists(t.Context(), key).Result(); err != nil || n != 0 {
				t.Fatal("skipped worker was given a native grant")
			}
			if len(planner.released) == 0 {
				t.Fatal("skipped worker kept its reservation")
			}
		})
	}
}

func TestNativeVideoRemoteStartsOnWorkerWithNativeStorage(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, _, _ := nativeVideoGrantFixture(t, client)
	service.WorkerGrants = NewNativeVideoWorkerGrantStore(client)
	file, err := service.Files.GetByID(t.Context(), session.MediaFileID)
	if err != nil {
		t.Fatal(err)
	}
	starts, probes := &atomic.Int32{}, &atomic.Int32{}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == transcodenode.NativeExecutionReadinessPath {
			probes.Add(1)
			if r.Header.Get("Authorization") == "" {
				t.Error("readiness probe was not signed")
			}
		}
		if answerNativeExecutionReadiness(w, r, 7) {
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/hw-capabilities":
			writeJSON(w, http.StatusOK, playback.HWAccelInfo{Transformations: remoteHLSPlanV3().Transformations})
		case r.Method == http.MethodPost && r.URL.Path == "/transcode/start":
			starts.Add(1)
			var req transcodenode.TranscodeStartRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			writeJSON(w, http.StatusAccepted, transcodenode.TranscodeStartResponse{SessionID: req.SessionID, Status: "started", HWAccel: req.HWAccel, AudioRecipeVersion: req.AudioRecipeVersion, CopyFMP4RecipeVersion: req.CopyFMP4RecipeVersion, ThrottleSeconds: req.ThrottleSeconds})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer worker.Close()
	h := NewPlaybackHandler(service.Sessions, service.Files)
	h.NativeVideo, h.JWTSecret = service, "fixture-secret"
	h.NodePlanner = &progressiveFallbackPlannerV3{transcode: &nodepool.Node{ID: 7, URL: worker.URL}, proxy: &nodepool.Node{ID: 8, URL: "http://proxy.example"}}
	result := remoteHLSResultV3()
	result.Plan.SessionID, result.Plan.EffectiveMediaFileID = session.ID, file.ID
	policy := config.DefaultPlaybackRoutingPolicy()
	policy.VideoTranscodeExecution = config.PlaybackExecutionWorkerOnly
	policy.VideoTranscodeEgress = config.PlaybackEgressAPIOnly
	for range 2 {
		transport, failure := h.prepareTransportWithPolicyV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, mediaAuthModeV3{}, policy)
		if failure != nil {
			t.Fatalf("ready worker refused: %v", failure)
		}
		transport.rollback()
	}
	if starts.Load() != 2 || probes.Load() != 1 {
		t.Fatalf("starts=%d probes=%d; want two starts and one cached probe", starts.Load(), probes.Load())
	}
}
