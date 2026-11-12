package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

func compatWorkerRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("SILO_TEST_REDIS_URL")
	if url == "" {
		t.Skip("SILO_TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal("invalid test Redis configuration")
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("test Redis unavailable: %v", err)
	}
	return client
}

// compatWorker answers the native-execution readiness probe as worker id.
func compatWorker(t *testing.T, id int, probes *atomic.Int32) *httptest.Server {
	t.Helper()
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != transcodenode.NativeExecutionReadinessPath || r.Header.Get("Authorization") == "" {
			http.NotFound(w, r)
			return
		}
		probes.Add(1)
		if id <= 0 {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(transcodenode.NativeExecutionReadiness{NativeExecution: true, NodeID: id})
	}))
	t.Cleanup(worker.Close)
	return worker
}

func (f *compatServeFixture) workerContext() context.Context {
	ctx := playback.WithContextMediaHost(context.Background(), f.host)
	playback.NoteContextStream(ctx, f.stream)
	return ctx
}

func (f *compatServeFixture) start() (transcodenode.TranscodeStartRequest, []byte) {
	start := transcodenode.TranscodeStartRequest{SessionID: f.stream.PlaybackSessionID, InputPath: f.delegation.base.file.FilePath, TargetCodecVideo: "h264", TargetCodecAudio: "aac", SegmentDuration: 6}
	body, _ := json.Marshal(start)
	return start, body
}

// Phase 3: a Jellyfin remote HLS start seals the worker grant for the exact
// start; the stored recipe is accepted for segment reads; a stop revokes it.
func TestNativeVideoCompatWorkerStartSealsAndAcceptsRecipe(t *testing.T) {
	client := compatWorkerRedis(t)
	f := newCompatServeFixtureFor(t, uuid.NewString())
	f.service.WorkerGrants = handlers.NewNativeVideoWorkerGrantStore(client)
	probes := &atomic.Int32{}
	worker := compatWorker(t, 7, probes)
	start, body := f.start()
	if err := f.host.SealWorkerStart(f.workerContext(), worker.URL, body); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if probes.Load() != 1 {
		t.Fatalf("readiness probes = %d, want 1", probes.Load())
	}
	grants := f.service.WorkerGrants
	if _, err := grants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Start: &start}, f.service); err != nil {
		t.Fatalf("worker refused the sealed start: %v", err)
	}
	other := start
	other.TargetCodecVideo = "hevc"
	if _, err := grants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Start: &other}, f.service); err == nil {
		t.Fatal("grant admitted a different start")
	}
	if _, err := grants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 8, TransportID: start.SessionID, InputPath: start.InputPath, Start: &start}, f.service); err == nil {
		t.Fatal("grant admitted another worker")
	}

	session := f.delegation.base.session
	card := playback.NewRecipeCard(session.UserID, session.ProfileID, session.MediaFileID, worker.URL, playback.TranscodeOpts{SessionID: start.SessionID, InputPath: start.InputPath, TargetCodecVideo: "h264", TargetCodecAudio: "aac", SegmentDuration: 6})
	accepted, err := f.host.AcceptWorkerRecipe(t.Context(), card)
	if err != nil {
		t.Fatalf("accept recipe: %v", err)
	}
	if accepted.RoutingExecutionNodeID != 7 {
		t.Fatalf("accepted recipe names worker %d, want 7", accepted.RoutingExecutionNodeID)
	}
	if _, err := grants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &accepted}, f.service); err != nil {
		t.Fatalf("worker refused the stored recipe: %v", err)
	}
	moved := card
	moved.RoutingExecutionNodeID = 8
	if _, err := f.host.AcceptWorkerRecipe(t.Context(), moved); err == nil {
		t.Fatal("recipe for another worker was accepted")
	}

	stopped := *session
	f.delegation.base.session = nil
	f.host.revokeStopped(t.Context(), &stopped)
	if _, err := grants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &accepted}, f.service); err == nil {
		t.Fatal("stopped session's grant still resolves")
	}
}

func TestNativeVideoCompatWorkerStartRefusals(t *testing.T) {
	for _, kind := range []string{"ordinary-input", "worker-not-ready", "other-session", "signed-out", "no-stream"} {
		t.Run(kind, func(t *testing.T) {
			client := compatWorkerRedis(t)
			f := newCompatServeFixtureFor(t, uuid.NewString())
			f.service.WorkerGrants = handlers.NewNativeVideoWorkerGrantStore(client)
			probes := &atomic.Int32{}
			id := 7
			if kind == "worker-not-ready" {
				id = 0
			}
			worker := compatWorker(t, id, probes)
			start, body := f.start()
			ctx := f.workerContext()
			wantErr := true
			switch kind {
			case "ordinary-input":
				start.InputPath = "/media/movie.mkv"
				body, _ = json.Marshal(start)
				wantErr = false
			case "other-session":
				start.SessionID = uuid.NewString()
				body, _ = json.Marshal(start)
			case "signed-out":
				f.sessions.err = errors.New("signed out")
			case "no-stream":
				ctx = playback.WithContextMediaHost(context.Background(), f.host)
			}
			err := f.host.SealWorkerStart(ctx, worker.URL, body)
			if (err != nil) != wantErr {
				t.Fatalf("seal err = %v, want error %v", err, wantErr)
			}
			if _, resolveErr := f.service.WorkerGrants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Start: &start}, f.service); resolveErr == nil {
				t.Fatal("a refused start left a usable grant")
			}
		})
	}
}
