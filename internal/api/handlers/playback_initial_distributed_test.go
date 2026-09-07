package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/playback/planstore"
	"github.com/Silo-Server/silo-server/internal/proxy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type initialReservationPlanner struct {
	enumeratingNodePlannerV3
	released chan string
}

func (p *initialReservationPlanner) ReleaseSession(sessionID string) {
	p.released <- sessionID
}

// Real configured-node identity comes from the watcher and its isolated database
// row. No worker/proxy test-only identity setter substitutes for production setup.
func initialDistributedNode(t *testing.T, f *initialHTTPFixture, kind, ffmpeg string, loseReadyReply bool, lostStartNumber ...int32) (*nodepool.Node, *atomic.Int32) {
	t.Helper()
	var handler http.Handler
	starts := new(atomic.Int32)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/transcode/") {
			t.Error("legacy DELETE used for bound worker")
		}

		if r.Method == http.MethodPost && r.URL.Path == "/transcode/start" {
			starts.Add(1)
		}
		if r.URL.Path == "/transcode/prepare" || r.URL.Path == "/transcode/start" {
			capture := httptest.NewRecorder()
			handler.ServeHTTP(capture, r)
			if capture.Code != http.StatusOK && capture.Code != http.StatusAccepted {
				t.Logf("selected node %s status=%d body=%s", r.URL.Path, capture.Code, capture.Body.String())
			}
			if (loseReadyReply || len(lostStartNumber) > 0 && starts.Load() == lostStartNumber[0]) && r.URL.Path == "/transcode/start" && capture.Code == http.StatusAccepted {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
				return
			}

			for name, values := range capture.Header() {
				for _, value := range values {
					w.Header().Add(name, value)
				}
			}
			w.WriteHeader(capture.Code)
			_, _ = w.Write(capture.Body.Bytes())
			return
		}
		handler.ServeHTTP(w, r)
	}))
	nodeURL := "http://" + server.Listener.Addr().String()
	var id int
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO stream_nodes(name,type,url,enabled) VALUES($1,$2,$3,true) RETURNING id`, uuid.NewString(), kind, nodeURL).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		server.Close()
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM stream_nodes WHERE id=$1`, id)
	})
	options, err := redis.ParseURL(os.Getenv("SILO_TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	control := f.flow.Control.(*planstore.Postgres)
	recipes := f.flow.Recipes.(*noderecipe.Store)
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: time.Second, SafetyMargin: 100 * time.Millisecond, RenewBefore: 200 * time.Millisecond, PollInterval: time.Millisecond}
	runtime, err := planstore.NewExecutorRuntime(control, recipes, id, f.flow.Clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte("synthetic-initial-node-key-32-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	watcher := nodeconfig.NewWatcher(f.pool, cipher, nil, nodeconfig.BootstrapOverrides{NodeURL: nodeURL, Mode: kind})
	root := t.TempDir()
	watcher.OnLoad(func(cfg *config.Config) {
		cfg.Auth.JWTSecret = f.handler.JWTSecret
		cfg.Playback.FFmpegPath = ffmpeg
		cfg.Playback.TranscodeDir = root
		cfg.Playback.HWAccel = playback.HWAccelNone
	})
	if err := watcher.ForceReload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, ok := watcher.NodeRowID(); !ok || got != id {
		t.Fatal("watcher did not bind selected node")
	}
	tracker := nodesessions.NewTracker(client, nodeURL, "synthetic", kind)
	if kind == "transcode" {
		worker := transcodenode.NewServer(watcher, tracker).WithExecutorGrantProvider(runtime.Acquire).WithExecutorRecipeResolver(runtime.Resolve).WithExecutorOutputTransferProvider(runtime.AcquireOutputTransfer)
		worker.SetInputPathAuthorizer(transcodenode.NewCatalogPathAuthorizer(scanner.NewFileRepository(f.pool)))
		handler = worker.Handler()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := worker.Shutdown(ctx); err != nil {
				t.Error(err)
			}
		})
	} else {
		egress := proxy.NewServer(watcher, tracker).WithExecutorRuntime(runtime.Acquire, runtime.Resolve, runtime.OpenOutputTransfer)
		egress.SetClientIPResolver(clientip.NewResolver(nil))
		handler = egress.Handler()
	}
	server.Start()
	return &nodepool.Node{ID: id, Type: kind, URL: nodeURL, Enabled: true, Healthy: true}, starts
}

func TestInitialPlaybackHTTPDistributed(t *testing.T) {
	for _, topology := range []string{"worker-api", "worker-proxy", "direct-proxy", "worker-api-lost-reply", "worker-api-successor", "worker-proxy-successor", "direct-proxy-successor", "worker-api-successor-lost"} {
		t.Run(topology, func(t *testing.T) {
			loseSuccessorReply := strings.HasSuffix(topology, "-successor-lost")
			if loseSuccessorReply {
				topology = strings.TrimSuffix(topology, "-lost")
			}
			successorRun := strings.HasSuffix(topology, "-successor")
			topology = strings.TrimSuffix(topology, "-successor")
			loseReadyReply := strings.HasSuffix(topology, "-lost-reply")
			topology := strings.TrimSuffix(topology, "-lost-reply")

			f := newInitialHTTPFixture(t)
			ffmpeg, err := exec.LookPath("ffmpeg")
			if err != nil {
				t.Fatal("distributed fixture requires ffmpeg")
			}
			policy := config.DefaultPlaybackRoutingPolicy()
			policy.VideoTranscodeExecution = config.PlaybackExecutionWorkerOnly
			policy.VideoTranscodeEgress = config.PlaybackEgressAPIOnly
			policy.DirectPlayEgress = config.PlaybackEgressProxyOnly
			plan := nodepool.Plan{}
			var starts *atomic.Int32
			if topology != "direct-proxy" {
				cmd := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "42", "-c:v", "mpeg4", "-c:a", "aac", f.file.FilePath)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("source: %v %s", err, output)
				}
				f.file.CodecVideo = "mpeg4"
				f.file.Duration = 42
				f.file.Resolution = "180p"
				f.file.VideoTracks = []models.VideoTrack{{Codec: "mpeg4", Width: 320, Height: 180, FrameRate: "24/1", BitDepth: 8, VideoRange: "SDR"}}
				f.request.ClientPlaybackContext.Deliveries[playback.DeliveryClassHLSV3] = playback.DeliveryCapabilityV3{Enabled: true, SupportedOnDevice: true}
				if loseSuccessorReply {
					plan.TranscodeNode, starts = initialDistributedNode(t, f, "transcode", ffmpeg, false, 2)
				} else {
					plan.TranscodeNode, starts = initialDistributedNode(t, f, "transcode", ffmpeg, loseReadyReply)
				}
			}
			if topology != "worker-api" {
				plan.ProxyNode, _ = initialDistributedNode(t, f, "proxy", ffmpeg, false)
				policy.VideoTranscodeEgress = config.PlaybackEgressProxyOnly
			}
			planner := enumeratingNodePlannerV3{staticNodePlannerV3: staticNodePlannerV3{plan: plan}}
			if plan.TranscodeNode != nil {
				planner.urls = []string{plan.TranscodeNode.URL}
			}
			if plan.ProxyNode != nil {
				planner.proxyURLs = []string{plan.ProxyNode.URL}
			}
			reservations := &initialReservationPlanner{enumeratingNodePlannerV3: planner, released: make(chan string, 16)}
			f.handler.NodePlanner = reservations
			apiOutput := t.TempDir()
			f.handler.PlaybackConfig = func() config.PlaybackConfig {
				return config.PlaybackConfig{TranscodeEnabled: true, HWAccel: "none", FFmpegPath: ffmpeg, TranscodeDir: apiOutput, Routing: policy}
			}
			status, data := f.call(t, http.MethodPost, "/start", f.request)
			if loseReadyReply {
				if status != http.StatusServiceUnavailable || starts.Load() != 1 {
					t.Fatalf("uncertain start status=%d starts=%d body=%s", status, starts.Load(), data)
				}
				var phase, sessionID string
				if err := f.pool.QueryRow(t.Context(), `SELECT control_activation->>'phase',session_id::text FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.request.PlaybackAttemptID).Scan(&phase, &sessionID); err != nil {
					t.Fatal(err)
				}
				if phase != "aborting" && phase != "aborted" {
					t.Fatalf("uncertain executor became %s", phase)
				}
				if _, err := f.manager.GetSession(sessionID); !errors.Is(err, playback.ErrSessionNotFound) {
					t.Fatal("uncertain start published session", err)
				}
				status, data = f.call(t, http.MethodPost, "/start", f.request)
				if status != http.StatusServiceUnavailable || starts.Load() != 1 {
					t.Fatalf("uncertain start replay status=%d starts=%d body=%s", status, starts.Load(), data)
				}
				return
			}

			if status != http.StatusCreated {
				t.Fatalf("start status=%d %s", status, data)
			}
			var decision playback.DecisionResponseV3
			if err := json.Unmarshal(data, &decision); err != nil {
				t.Fatal(err)
			}
			if decision.PlaybackPlan == nil {
				t.Fatal("missing initial plan")
			}
			cachedStatus, cachedData := f.call(t, http.MethodPost, "/start", f.request)
			var cached playback.DecisionResponseV3
			if err := json.Unmarshal(cachedData, &cached); err != nil {
				t.Fatal(err)
			}
			if cachedStatus < 200 || cachedStatus >= 300 || cached.SessionID != decision.SessionID || cached.PlaybackPlan == nil || cached.PlaybackPlan.PlanID != decision.PlaybackPlan.PlanID {
				t.Fatalf("initial replay changed receipt status=%d body=%s", cachedStatus, cachedData)
			}

			if starts != nil && starts.Load() != 1 {
				t.Fatalf("worker starts=%d", starts.Load())
			}
			if runtime := f.handler.tm.GetTranscodeSession(decision.SessionID); runtime != nil {
				t.Fatal("distributed route launched API runtime")
			}
			fetch := func(raw string) (int, []byte) {
				t.Helper()
				if !strings.HasPrefix(raw, "http") {
					raw = f.server.URL + raw
				}
				request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, raw, nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				if response.Header.Get(playback.OutputTransferHeaderV3) != "" {
					t.Fatal("permit escaped")
				}
				return response.StatusCode, body
			}
			expectedStarts := int32(1)
			if successorRun {
				originalURL := decision.PlaybackPlan.Stream.URL
				originalPlan := decision.PlaybackPlan.PlanID
				ctx := initialContextV3(t, f)
				command := replanCommandV3(t, initialReplanRequestV3(decision, f.request, uuid.NewString(), 6))
				next, err := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), decision.SessionID, command)
				if loseSuccessorReply {
					if err == nil || starts.Load() != 2 {
						t.Fatalf("uncertain candidate start: starts=%d err=%v", starts.Load(), err)
					}
					control := &initialUncertainCancellationControl{InitialPlaybackControlV3: f.flow.Control, BoundReplanStoreV3: f.flow.Control.(BoundReplanStoreV3), BoundRouteReplacementStoreV3: f.flow.Control.(playback.BoundRouteReplacementStoreV3), denyConfirmation: true}
					f.flow.Control = control
					if _, err := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), decision.SessionID, command); err == nil || starts.Load() != 2 {
						t.Fatal("uncertain candidate was replayed")
					}
					if status, _ := fetch(originalURL); status != 200 {
						t.Fatal("cancelled candidate revoked predecessor")
					}
					var phase string
					if err := f.pool.QueryRow(ctx, `SELECT route_replacement->>'phase' FROM playback_v3_replans WHERE session_id=$1 AND replan_request_id=$2`, decision.SessionID, command.Request.ReplanRequestID).Scan(&phase); err != nil || phase != "cancelled" {
						t.Fatalf("candidate cancellation: %s %v", phase, err)
					}
					// A lost cancellation observation keeps the old blocker, even
					// though the candidate's persisted state is already cancelled.
					fresh := replanCommandV3(t, initialReplanRequestV3(decision, f.request, "fresh-seek-after-cancelled", 8))
					_, freshErr := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), decision.SessionID, fresh)
					requirePlaybackOperationError(t, freshErr, http.StatusConflict, "replan_in_progress")
					if starts.Load() != 2 {
						t.Fatal("uncertain cancellation allowed another launch")
					}
					control.denyConfirmation = false
					deadline := time.NewTimer(5 * time.Second)
					defer deadline.Stop()
					poll := time.NewTicker(10 * time.Millisecond)
					defer poll.Stop()
					var recovered playback.DecisionResponseV3
					for {
						recovered, freshErr = f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), decision.SessionID, fresh)
						if freshErr == nil {
							break
						}
						requirePlaybackOperationError(t, freshErr, http.StatusConflict, "replan_in_progress")
						select {
						case <-deadline.C:
							t.Fatal("confirmed cancellation permanently blocked fresh seek")
						case <-poll.C:
						}
					}
					if recovered.PlaybackPlan == nil || recovered.PlaybackPlan.PlanID == originalPlan || starts.Load() != 3 {
						t.Fatal("fresh seek did not launch exactly one new candidate")
					}
					if _, err := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), decision.SessionID, command); err == nil || starts.Load() != 3 {
						t.Fatal("old cancelled key launched or affected the new executor")
					}
					if status, _ := fetch(recovered.PlaybackPlan.Stream.URL); status != http.StatusOK {
						t.Fatal("cancelled retry cleanup revoked fresh successor")
					}
					stop := PlaybackStopCommand{StopID: uuid.NewString()}
					stopDeadline := time.Now().Add(5 * time.Second)
					for {
						view, err := f.handler.StopInitialPlayback(ctx, initialCallerV3(f), decision.SessionID, stop)
						if err != nil {
							t.Fatal(err)
						}
						if !view.Draining {
							break
						}
						if time.Now().After(stopDeadline) {
							t.Fatal("cancelled candidate did not drain at stop")
						}
					}
					return
				}
				if err != nil || next.PlaybackPlan == nil || next.PlaybackPlan.PlanID == originalPlan {
					t.Fatalf("distributed successor: %+v %v", next, err)
				}
				if status, _ := fetch(originalURL); status == 200 {
					t.Fatal("retired generation serves after cutover")
				}
				replay, err := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), decision.SessionID, command)
				if err != nil || replay.PlaybackPlan == nil || replay.PlaybackPlan.PlanID != next.PlaybackPlan.PlanID {
					t.Fatalf("successor replay: %+v %v", replay, err)
				}
				decision = next
				expectedStarts = 2
				if starts != nil && starts.Load() != expectedStarts {
					t.Fatalf("candidate start count=%d", starts.Load())
				}
				if f.handler.tm.GetTranscodeSession(decision.SessionID) != nil {
					t.Fatal("remote successor launched API runtime")
				}
			}
			current := decision.PlaybackPlan.Stream.URL
			for depth := 0; depth < 4; depth++ {
				status, data = fetch(current)
				if status != 200 || len(data) == 0 {
					t.Fatalf("bytes status=%d body=%s", status, data)
				}
				if !bytes.Contains(data, []byte("#EXTM3U")) {
					break
				}
				base, err := url.Parse(current)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				elapsed, segmentDuration := 0.0, 0.0
				for line := range strings.SplitSeq(string(data), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "#EXTINF:") {
						segmentDuration, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","), 64)
					}
					if line != "" && !strings.HasPrefix(line, "#") {
						if successorRun && segmentDuration > 0 && elapsed+segmentDuration <= decision.PlaybackPlan.Timeline.PlayerStartSeconds {
							elapsed += segmentDuration
							continue
						}
						ref, err := url.Parse(line)
						if err != nil {
							t.Fatal(err)
						}
						current = base.ResolveReference(ref).String()
						found = true
						break
					}
				}
				if !found || depth == 3 {
					t.Fatal("playlist did not reach media")
				}
			}
			if successorRun && starts != nil {
				earlier := strings.Replace(current, "seg_00003", "seg_00000", 1)
				if earlier == current {
					t.Fatal("restored position did not reach segment3")
				}
				if status, _ := fetch(earlier); status == 200 || starts.Load() != 2 {
					t.Fatal("out-of-generation segment reconstructed executor")
				}
			}
			status, data = f.call(t, http.MethodPost, "/playback/"+decision.SessionID+"/progress", map[string]any{"sequence": 1, "position": 5, "is_paused": false})
			if status != 200 {
				t.Fatalf("progress status=%d %s", status, data)
			}
			select {
			case id := <-reservations.released:
				t.Fatalf("active session reservation released: %s", id)
			default:
			}
			stop := map[string]any{"stop_id": uuid.NewString(), "sequence": 2, "position": 6, "is_paused": true}
			deadline := time.Now().Add(5 * time.Second)
			for {
				status, data = f.call(t, http.MethodDelete, "/playback/"+decision.SessionID, stop)
				if status == 200 {
					break
				}
				if status != 202 || time.Now().After(deadline) {
					t.Fatalf("stop status=%d %s", status, data)
				}
			}
			select {
			case id := <-reservations.released:
				if id != decision.SessionID {
					t.Fatalf("released wrong terminal session: %s", id)
				}
			default:
				t.Fatal("terminal session reservation retained")
			}
			status, data = fetch(current)
			if status == 200 {
				t.Fatalf("terminal route still serves %d bytes", len(data))
			}
			if starts != nil && starts.Load() != expectedStarts {
				t.Fatal("stop/re-fetch replayed worker start")
			}
			if topology == "worker-api" && strings.HasPrefix(decision.PlaybackPlan.Stream.URL, "http") {
				t.Fatal("API egress URL escaped to worker")
			}
			if topology != "worker-api" && !strings.HasPrefix(decision.PlaybackPlan.Stream.URL, plan.ProxyNode.URL+"/") {
				t.Fatal("proxy route published wrong origin")
			}
		})
	}
}
