package transcodenode

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestBoundProgressiveWorkerExactPrepareStartOutput(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run("lostReady="+strconv.FormatBool(lost), func(t *testing.T) { testBoundProgressiveWorker(t, lost) })
	}
}

func testBoundProgressiveWorker(t *testing.T, lost bool) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.m4a")
	if out, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000", "-t", "2", "-c:a", "aac", source).CombinedOutput(); err != nil {
		t.Fatalf("source: %v %s", err, out)
	}
	ns := workerNamespace()
	card := playback.RecipeCard{Executor: ns, SessionID: "session", TranscodeTransportID: "transport", UserID: 1, ProfileID: "profile", MediaFileID: 2, PlayMethod: playback.PlayRemux, AudioOnly: true, RemuxDVMode: playback.RemuxDVPreserveV3, InputPath: source, TotalDuration: 2, RoutingWorkload: "remux", RoutingExecution: "transcode", RoutingExecutionNodeID: 1, RoutingEgress: "api"}
	var executes, transfers atomic.Int32
	execute := workerGrantProvider(t)
	registry := playback.NewBoundProgressiveRegistryV3(t.Context(), func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		if purpose != playback.AttemptGrantExecuteV3 {
			t.Error("wrong execution purpose")
		}
		executes.Add(1)
		return execute(ctx, transport, executor, purpose)
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := registry.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	handler := &BoundProgressiveHTTPV3{NodeID: func() (int, bool) { return 1, true }, ApproveSource: func(_ context.Context, path string) error {
		if path != source {
			return errors.New("unapproved")
		}
		return nil
	}, Registry: registry, OutputRoot: t.TempDir(), FFmpegPath: bin}
	handler.Resolve = func(_ context.Context, transport string, executor playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		if transport != card.TranscodeTransportID || executor != *card.Executor {
			return nil, errors.New("foreign")
		}
		copy := card
		return &copy, nil
	}
	handler.Transfers = func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, permit string) (*playback.RuntimeGrantV3, error) {
		if permit != "owned-permit" {
			return nil, errors.New("foreign permit")
		}
		transfers.Add(1)
		return workerTestTransferGrant(t, ctx, clock, executor, transport, permit, nil)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /remux/prepare", handler.Prepare)
	mux.HandleFunc("POST /remux/start", func(w http.ResponseWriter, r *http.Request) {
		result := httptest.NewRecorder()
		handler.Start(result, r)
		if lost && result.Code == http.StatusAccepted {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
			return
		}
		for key, values := range result.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(result.Code)
		_, _ = w.Write(result.Body.Bytes())
	})
	mux.HandleFunc("GET /remux/output/{transport}", handler.Output)
	worker := httptest.NewServer(mux)
	defer worker.Close()
	card.TranscodeNodeURL = worker.URL
	endpoint, _ := BoundProgressiveOutputEndpointV3(card)
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 || executes.Load() != 0 {
		t.Fatal("GET created runtime")
	}
	client := BoundProgressiveClientV3{Bearer: "synthetic-node"}
	if err := client.Start(t.Context(), card); err == nil {
		t.Fatal("start without retained prepare accepted")
	}
	prepared, err := client.Prepare(t.Context(), card)
	if err != nil {
		t.Fatal(err)
	}
	if executes.Load() != 0 {
		t.Fatal("prepare executed")
	}
	original, _ := playback.BoundProgressiveRecipeDigestV3(card)
	got, _ := playback.BoundProgressiveRecipeDigestV3(prepared)
	if original != got {
		t.Fatal("prepare changed captured clock/source")
	}
	changed := card
	changed.SeekSeconds = 1
	if err := client.Start(t.Context(), changed); err == nil {
		t.Fatal("changed recipe accepted")
	}
	if err := client.Start(t.Context(), card); (err != nil) != lost {
		t.Fatalf("lostReady=%v result=%v", lost, err)
	}
	if err := client.Start(t.Context(), card); err == nil {
		t.Fatal("start replay accepted")
	}
	if executes.Load() != 1 {
		t.Fatal("start count", executes.Load())
	}
	var released atomic.Bool
	egress := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := playback.ServeRemoteBoundProgressiveV3(w, r, card, endpoint, "synthetic-node", workerGrantProvider(t), func(context.Context, string, playback.ExecutorNamespaceV3) (string, func(), error) {
			return "owned-permit", func() { released.Store(true) }, nil
		})
		if err != nil {
			t.Error(err)
		}
	}))
	defer egress.Close()
	response, err = http.Get(egress.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Contains(data, []byte("mdat")) {
		t.Fatalf("egress bytes: %d %v", len(data), err)
	}
	if transfers.Load() != 1 {
		t.Fatal("missing output transfer", transfers.Load())
	}
	// A terminal stop must remove only this retained producer; no remote DELETE.
	if err := registry.Stop(t.Context(), card.TranscodeTransportID, *card.Executor); err != nil {
		t.Fatal(err)
	}
	if !released.Load() {
		t.Fatal("transfer not released")
	}
}

func TestBoundProgressiveCommandsNeverFollowRedirectOrRetry(t *testing.T) {
	var called atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Add(1) }))
	defer destination.Close()
	var requests atomic.Int32
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer worker.Close()
	ns := workerNamespace()
	card := playback.RecipeCard{Executor: ns, SessionID: "s", TranscodeTransportID: "t", UserID: 1, ProfileID: "p", MediaFileID: 1, PlayMethod: playback.PlayRemux, InputPath: "synthetic", TotalDuration: 2, RemuxDVMode: playback.RemuxDVPreserveV3, RoutingWorkload: "remux", RoutingExecution: "transcode", RoutingExecutionNodeID: 1, TranscodeNodeURL: worker.URL, RoutingEgress: "api"}
	client := BoundProgressiveClientV3{}
	if _, err := client.Prepare(t.Context(), card); err == nil {
		t.Fatal("redirect accepted")
	}
	if err := client.Start(t.Context(), card); err == nil {
		t.Fatal("redirect accepted")
	}
	if called.Load() != 0 || requests.Load() != 2 {
		t.Fatalf("redirect/retry: %d %d", called.Load(), requests.Load())
	}
	command, _ := BoundProgressiveCommandForRecipeV3(card)
	ready := BoundProgressiveReadyV3{BoundProgressiveCommandV3: command, Status: "ready"}
	ready.RecipeDigest = "changed"
	if ValidateBoundProgressiveReadyV3(command, ready) == nil {
		t.Fatal("foreign ready accepted")
	}
}

func TestBoundProgressiveWorkerRejectsMalformedPrepare(t *testing.T) {
	handler := &BoundProgressiveHTTPV3{}
	for _, body := range []string{`{} {}`, `{"recipe":{},"unknown":true}`} {
		response := httptest.NewRecorder()
		handler.Prepare(response, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)))
		if response.Code != 400 {
			t.Fatal(response.Code)
		}
	}
}
