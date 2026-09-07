package playback

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func progressiveFixture(t *testing.T) (*PreparedBoundProgressiveV3, string) {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("focused progressive tests require ffmpeg", err)
	}
	source := filepath.Join(t.TempDir(), "source.m4a")
	cmd := exec.CommandContext(t.Context(), bin, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "2", "-c:a", "aac", source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic source: %v %s", err, out)
	}
	ns := executorFixture()
	card := RecipeCard{Executor: &ns, SessionID: "logical", TranscodeTransportID: "transport", UserID: 1, ProfileID: "profile", MediaFileID: 1, PlayMethod: PlayRemux, InputPath: source, AudioOnly: true, TotalDuration: 2, RoutingWorkload: "remux", RoutingExecution: "api", RoutingEgress: "api"}
	root := t.TempDir()
	p, err := PrepareBoundProgressiveV3(t.Context(), card, root, bin)
	if err != nil {
		t.Fatal(err)
	}
	return p, root
}

func closeProgressive(t *testing.T, r *BoundProgressiveRegistryV3) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Error(err)
	}
}

func TestBoundProgressiveActualOutputAndOneShot(t *testing.T) {
	p, root := progressiveFixture(t)
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("prepare claimed output: %v %v", entries, err)
	}
	card := p.Recipe()
	card.Executor.Epoch++
	if p.Recipe().Executor.Epoch == card.Executor.Epoch {
		t.Fatal("recipe alias")
	}
	registry := NewBoundProgressiveRegistryV3(t.Context(), executorGrantTestProvider(nil))
	defer closeProgressive(t, registry)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := registry.Start(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := registry.Start(ctx, p); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatalf("start replay: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := registry.ServeHTTP(w, r, p.card.TranscodeTransportID, *p.card.Executor, executorGrantTestProvider(nil), AttemptGrantServeV3); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || !bytes.Contains(output, []byte("moov")) || !bytes.Contains(output, []byte("mdat")) {
		t.Fatalf("actual output %d %v", len(output), err)
	}
	rendered := filepath.Join(t.TempDir(), "rendered.mp4")
	if err := os.WriteFile(rendered, output, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), p.binary, "-v", "error", "-i", rendered, "-f", "null", "-").CombinedOutput(); err != nil {
		t.Fatalf("undecodable remux: %v %s", err, out)
	}
	foreign := *p.card.Executor
	foreign.Epoch++
	if err := registry.Stop(ctx, p.card.TranscodeTransportID, foreign); !errors.Is(err, ErrBoundProgressiveMissingV3) {
		t.Fatalf("foreign stop: %v", err)
	}
	if err := registry.Stop(ctx, p.card.TranscodeTransportID, *p.card.Executor); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output survived stop: %v", err)
	}
	if err := registry.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), p.card.TranscodeTransportID, *p.card.Executor, nil, AttemptGrantServeV3); !errors.Is(err, ErrBoundProgressiveMissingV3) {
		t.Fatalf("stopped served: %v", err)
	}
	restarted := NewBoundProgressiveRegistryV3(t.Context(), executorGrantTestProvider(nil))
	defer closeProgressive(t, restarted)
	if err := restarted.Start(ctx, p); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatalf("reused permanent claim: %v", err)
	}
}

func TestBoundProgressiveLostReadyKeepsExactRuntime(t *testing.T) {
	p, _ := progressiveFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	provider := executorGrantTestProvider(nil)
	registry := NewBoundProgressiveRegistryV3(t.Context(), func(ctx context.Context, transport string, ns ExecutorNamespaceV3, purpose AttemptGrantPurposeV3) (*RuntimeGrantV3, error) {
		close(entered)
		select {
		case <-release:
			return provider(ctx, transport, ns, purpose)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer closeProgressive(t, registry)
	request, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- registry.Start(request, p) }()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	e, err := registry.lookup(p.card.TranscodeTransportID, *p.card.Executor)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("lost ready killed producer")
	}
	if err := registry.Start(t.Context(), p); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatal("uncertain start relaunched", err)
	}
	if err := registry.Stop(t.Context(), p.card.TranscodeTransportID, *p.card.Executor); err != nil {
		t.Fatal(err)
	}
}

func TestBoundProgressiveGrantAndShutdownFence(t *testing.T) {
	p, root := progressiveFixture(t)
	denied := NewBoundProgressiveRegistryV3(t.Context(), func(context.Context, string, ExecutorNamespaceV3, AttemptGrantPurposeV3) (*RuntimeGrantV3, error) {
		return nil, errors.New("candidate denied")
	})
	if err := denied.Start(t.Context(), p); err == nil {
		t.Fatal("missing grant launched")
	}
	closeProgressive(t, denied)
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("denied execution claimed output")
	}
	ctx, cancel := context.WithCancel(t.Context())
	registry := NewBoundProgressiveRegistryV3(ctx, executorGrantTestProvider(nil))
	defer closeProgressive(t, registry)
	if err := registry.Start(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	cancel()
	e, _ := registry.lookup(p.card.TranscodeTransportID, *p.card.Executor)
	select {
	case <-e.done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join producer")
	}
	if _, err := os.Stat(p.output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("shutdown retained output", err)
	}
}

func TestBoundProgressiveMissingRuntimeNeverReconstructs(t *testing.T) {
	p, root := progressiveFixture(t)
	registry := NewBoundProgressiveRegistryV3(t.Context(), executorGrantTestProvider(nil))
	defer closeProgressive(t, registry)
	if err := registry.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), p.card.TranscodeTransportID, *p.card.Executor, nil, AttemptGrantServeV3); !errors.Is(err, ErrBoundProgressiveMissingV3) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("GET created runtime")
	}
	for _, method := range []PlayMethod{PlayDirect, PlayTranscode} {
		card := p.Recipe()
		card.PlayMethod = method
		if _, err := PrepareBoundProgressiveV3(t.Context(), card, root, p.binary); err == nil {
			t.Fatal("foreign family accepted", method)
		}
	}
}

func TestBoundProgressiveFailedLaunchAndLostReadinessConsumeNamespace(t *testing.T) {
	p, _ := progressiveFixture(t)
	if err := os.Remove(p.card.InputPath); err != nil {
		t.Fatal(err)
	}
	registry := NewBoundProgressiveRegistryV3(t.Context(), executorGrantTestProvider(nil))
	defer closeProgressive(t, registry)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := registry.Start(ctx, p); err == nil {
		t.Fatal("missing source claimed ready")
	}
	if err := registry.Start(ctx, p); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatal("failed process relaunched", err)
	}
	if err := registry.Stop(ctx, p.card.TranscodeTransportID, *p.card.Executor); err != nil {
		t.Fatal(err)
	}
	if err := claimExecutorOutput(p.output, *p.card.Executor); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatal("failed output namespace reused", err)
	}
}

func TestBoundProgressiveSuccessorCleanupAndGrantLoss(t *testing.T) {
	p, root := progressiveFixture(t)
	clock := new(executorGrantTestClock)
	registry := NewBoundProgressiveRegistryV3(t.Context(), executorGrantTestProvider(clock))
	defer closeProgressive(t, registry)
	if err := registry.Start(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	card := p.Recipe()
	card.Executor = new(executorFixture())
	card.TranscodeTransportID = "successor"
	card.SeekSeconds = 1
	card.StreamOriginSeconds = 1
	successor, err := PrepareBoundProgressiveV3(t.Context(), card, root, p.binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Start(t.Context(), successor); err != nil {
		t.Fatal(err)
	}
	if err := registry.Stop(t.Context(), p.card.TranscodeTransportID, *p.card.Executor); err != nil {
		t.Fatal(err)
	}
	retained, err := registry.RetainedRecipe(card.TranscodeTransportID, *card.Executor)
	if err != nil || retained.SeekSeconds != 1 || retained.StreamOriginSeconds != 1 {
		t.Fatal("successor clock changed", err)
	}
	if _, err := os.Stat(successor.output); err != nil {
		t.Fatal("old cleanup erased successor", err)
	}
	clock.elapsed.Store(int64(2 * time.Minute))
	e, _ := registry.lookup(card.TranscodeTransportID, *card.Executor)
	select {
	case <-e.done:
	case <-time.After(5 * time.Second):
		t.Fatal("grant loss retained producer")
	}
	if _, err := os.Stat(successor.output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("grant loss retained output", err)
	}
}

func TestBoundProgressiveRemoteReaderCancellationReleasesTransfer(t *testing.T) {
	p, _ := progressiveFixture(t)
	card := p.Recipe()
	card.RoutingExecution = "transcode"
	card.RoutingExecutionNodeID = 1
	workerCanceled := make(chan struct{})
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(OutputTransferHeaderV3) != "permit" {
			t.Error("transfer permit absent")
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("initial"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(workerCanceled)
	}))
	defer worker.Close()
	card.TranscodeNodeURL = worker.URL
	released := make(chan struct{})
	egress := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = ServeRemoteBoundProgressiveV3(w, r, card, worker.URL, "synthetic", executorGrantTestProvider(nil), func(context.Context, string, ExecutorNamespaceV3) (string, func(), error) {
			return "permit", func() { close(released) }, nil
		})
	}))
	defer egress.Close()
	response, err := http.Get(egress.URL)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len("initial"))
	if _, err := io.ReadFull(response.Body, data); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	select {
	case <-workerCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("reader disconnect left remote output live")
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("reader disconnect retained transfer permit")
	}
}

func TestBoundProgressiveVideoCopyProducesDecodableFragments(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "video.mp4")
	if out, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-t", "2", "-c:v", "libx264", "-threads", "1", "-g", "24", source).CombinedOutput(); err != nil {
		t.Fatalf("source: %v %s", err, out)
	}
	ns := executorFixture()
	card := RecipeCard{Executor: &ns, SessionID: "video", TranscodeTransportID: "video-output", UserID: 1, ProfileID: "p", MediaFileID: 2, PlayMethod: PlayRemux, InputPath: source, TotalDuration: 2, RemuxDVMode: RemuxDVPreserveV3, SourceVideoCodec: "h264", RoutingWorkload: "remux", RoutingExecution: "api", RoutingEgress: "api"}
	p, err := PrepareBoundProgressiveV3(t.Context(), card, t.TempDir(), bin)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewBoundProgressiveRegistryV3(t.Context(), executorGrantTestProvider(nil))
	defer closeProgressive(t, registry)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := registry.Start(ctx, p); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := registry.ServeHTTP(w, r, card.TranscodeTransportID, ns, executorGrantTestProvider(nil), AttemptGrantServeV3); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Content-Type") != "video/mp4" {
		t.Fatal("wrong video content type", response.Header)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "remux.mp4")
	if err := os.WriteFile(output, data, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-i", output, "-f", "null", "-").CombinedOutput(); err != nil {
		t.Fatalf("decode: %v %s", err, out)
	}
}

func TestBoundProgressiveAudioAACPreservesLocalSeekRecipe(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "surround.wav")
	if out, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-f", "lavfi", "-i", "anullsrc=channel_layout=5.1:sample_rate=48000", "-t", "3", "-c:a", "pcm_s16le", source).CombinedOutput(); err != nil {
		t.Fatalf("source: %v %s", err, out)
	}
	ns := executorFixture()
	card := RecipeCard{Executor: &ns, SessionID: "audio", TranscodeTransportID: "audio-output", UserID: 1, ProfileID: "p", MediaFileID: 3, PlayMethod: PlayRemux, InputPath: source, AudioOnly: true, TranscodeAudio: true, TargetCodecAudio: "aac", TargetAudioChannels: 2, TargetAudioBitrateKbps: 192, SourceAudioChannels: 6, TotalDuration: 3, SeekSeconds: 1, StreamOriginSeconds: 1, RemuxDVMode: RemuxDVPreserveV3, RoutingWorkload: "remux", RoutingExecution: "api", RoutingEgress: "api"}
	p, err := PrepareBoundProgressiveV3(t.Context(), card, t.TempDir(), bin)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := BoundProgressiveRecipeDigestV3(card)
	after, _ := BoundProgressiveRecipeDigestV3(p.Recipe())
	if before != after {
		t.Fatal("audio preparation rebased captured clock")
	}
	registry := NewBoundProgressiveRegistryV3(t.Context(), executorGrantTestProvider(nil))
	defer closeProgressive(t, registry)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := registry.Start(ctx, p); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := registry.ServeHTTP(w, r, card.TranscodeTransportID, ns, executorGrantTestProvider(nil), AttemptGrantServeV3); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Content-Type") != AudioOnlyRemuxMIMEV3 {
		t.Fatal("wrong audio content type", response.Header)
	}
	output := filepath.Join(t.TempDir(), "audio.mp4")
	if err := os.WriteFile(output, data, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), probe, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name,channels", "-of", "csv=p=0", output).CombinedOutput()
	if err != nil || string(bytes.TrimSpace(out)) != "aac,2" {
		t.Fatalf("audio adaptation: %s %v", out, err)
	}
}
