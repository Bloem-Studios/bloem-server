package transcodenode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func initialWorkerCard(t *testing.T) playback.RecipeCard {
	t.Helper()
	ns := workerNamespace()
	subdir, _ := ns.OutputSubdir()
	return playback.RecipeCard{Executor: ns, SessionID: "captured-session", TranscodeTransportID: "captured-transport", UserID: 1, ProfileID: "profile", MediaFileID: 1,
		PlayMethod: playback.PlayTranscode, RoutingWorkload: "video_transcode", RoutingExecution: "transcode", RoutingExecutionNodeID: 1,
		RoutingEgress: "api", InputPath: "/media/movie.mkv", OutputSubdir: subdir, SourceVideoCodec: "h264", TargetCodecVideo: "h264", TargetCodecAudio: "aac",
		HWAccel: playback.HWAccelNone, SegmentDuration: 2, TotalDuration: 12, FastStart: true, AudioTrackIndex: 0, SubtitleTrackIndex: -1}
}

func executorJSON(t *testing.T, client *http.Client, address string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, address, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testSecret)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestExecutorPreparationRealHTTPDoesNotLaunchOrPublish(t *testing.T) {
	s := newTestServer(t)
	s.watcher.Config().Playback.HWAccel = playback.HWAccelNone
	s.watcher.Config().Playback.FFmpegPath = "/not/a/real/ffmpeg"
	s.nodeRowID = func() (int, bool) { return 1, true }
	card := initialWorkerCard(t)
	var grants atomic.Int32
	s.WithExecutorGrantProvider(func(context.Context, string, playback.ExecutorNamespaceV3, playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		grants.Add(1)
		return nil, errors.New("not staged")
	})
	s.WithExecutorRecipeResolver(func(context.Context, string, playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		return nil, errors.New("not published")
	})
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	response := executorJSON(t, server.Client(), server.URL+"/transcode/prepare", ExecutorPreparation{Recipe: card})
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("prepare %d: %s", response.StatusCode, body)
	}
	var prepared ExecutorPreparation
	if err := json.NewDecoder(response.Body).Decode(&prepared); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExecutorPreparation(card, prepared.Recipe); err != nil {
		t.Fatal(err)
	}
	if grants.Load() != 0 || len(s.sessions) != 0 {
		t.Fatal("preparation acquired execution or registered session")
	}
	output, _ := card.Executor.OutputDir(s.transcodeDir)
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preparation claimed output: %v", err)
	}
	request, err := BoundTranscodeStartRequest(prepared.Recipe)
	if err != nil {
		t.Fatal(err)
	}
	response = executorJSON(t, server.Client(), server.URL+"/transcode/start", request)
	if response.StatusCode != http.StatusServiceUnavailable || grants.Load() != 0 {
		t.Fatal("unpublished recipe admitted execution")
	}
	for name, mutate := range map[string]func(*playback.RecipeCard){
		"node":      func(c *playback.RecipeCard) { c.RoutingExecutionNodeID++ },
		"namespace": func(c *playback.RecipeCard) { c.Executor = workerNamespace() },
		"transport": func(c *playback.RecipeCard) { c.TranscodeTransportID += "wrong" },
		"source":    func(c *playback.RecipeCard) { c.InputPath += "wrong" },
		"policy":    func(c *playback.RecipeCard) { c.ToneMapPolicy = "hardware" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := prepared.Recipe
			mutate(&changed)
			if err := ValidateExecutorPreparation(card, changed); err == nil {
				t.Fatal("changed preparation accepted")
			}
		})
	}
	foreign := card
	foreign.RoutingExecutionNodeID++
	response = executorJSON(t, server.Client(), server.URL+"/transcode/prepare", ExecutorPreparation{Recipe: foreign})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("foreign prepare %d", response.StatusCode)
	}
}

func TestExecutorBoundStartRejectsRecipeChangesAndLaunchesOnce(t *testing.T) {
	s := newTestServer(t)
	s.nodeRowID = func() (int, bool) { return 1, true }
	s.WithExecutorGrantProvider(workerGrantProvider(t))
	card := initialWorkerCard(t)
	s.WithExecutorRecipeResolver(func(context.Context, string, playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		return &card, nil
	})
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nprintf 'launch\\n' >> \"$0.calls\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.watcher.Config().Playback.FFmpegPath = ffmpeg
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	request, err := BoundTranscodeStartRequest(card)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TranscodeStartRequest){
		"input":     func(r *TranscodeStartRequest) { r.InputPath += "wrong" },
		"digest":    func(r *TranscodeStartRequest) { r.ExecutorRecipeDigest = "" },
		"readiness": func(r *TranscodeStartRequest) { r.RequireReady = false },
		"codec":     func(r *TranscodeStartRequest) { r.TargetCodecVideo = "hevc" },
		"seek":      func(r *TranscodeStartRequest) { r.SeekSeconds++ },
		"audio":     func(r *TranscodeStartRequest) { r.AudioTrackIndex++ },
		"namespace": func(r *TranscodeStartRequest) { r.Executor = workerNamespace() },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			mutate(&changed)
			response := executorJSON(t, server.Client(), server.URL+"/transcode/start", changed)
			if response.StatusCode != http.StatusConflict {
				t.Fatalf("changed request status %d", response.StatusCode)
			}
		})
	}
	if _, err := os.Stat(ffmpeg + ".calls"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected request launched process")
	}
	response := executorJSON(t, server.Client(), server.URL+"/transcode/start", request)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failed startup status %d", response.StatusCode)
	}
	calls, err := os.ReadFile(ffmpeg + ".calls")
	if err != nil || string(calls) != "launch\n" {
		t.Fatalf("launches %q: %v", calls, err)
	}
	if len(s.sessions) != 0 {
		t.Fatal("failed readiness registered executor")
	}
}

func TestExecutorStartReceiptRequiresExactReadyRecipe(t *testing.T) {
	request, err := BoundTranscodeStartRequest(initialWorkerCard(t))
	if err != nil {
		t.Fatal(err)
	}
	good := TranscodeStartResponse{Executor: request.Executor, ExecutorRecipeDigest: request.ExecutorRecipeDigest, SessionID: request.SessionID, Status: "started", HWAccel: request.HWAccel}
	if err := ValidateBoundTranscodeStartResponse(request, good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TranscodeStartResponse){
		"empty202":  func(r *TranscodeStartResponse) { *r = TranscodeStartResponse{} },
		"foreign":   func(r *TranscodeStartResponse) { r.Executor = workerNamespace() },
		"changed":   func(r *TranscodeStartResponse) { r.ExecutorRecipeDigest += "wrong" },
		"transport": func(r *TranscodeStartResponse) { r.SessionID += "wrong" },
		"hardware":  func(r *TranscodeStartResponse) { r.HWAccel = "videotoolbox" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := good
			mutate(&changed)
			if err := ValidateBoundTranscodeStartResponse(request, changed); err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
}

func TestExecutorBoundStartReadyReceiptAndDuplicateRefusal(t *testing.T) {
	s := newTestServer(t)
	s.nodeRowID = func() (int, bool) { return 1, true }
	s.tracker = &recordingSessionTracker{}
	s.WithExecutorGrantProvider(workerGrantProvider(t))
	card := initialWorkerCard(t)
	s.WithExecutorRecipeResolver(func(context.Context, string, playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		return &card, nil
	})
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	// This fixture exercises process admission and HTTP readiness, not media
	// correctness. The synthetic manifest is observable before the reply.
	script := `#!/bin/sh
printf 'launch\n' >> "$0.calls"
for output do :; done
dir="${output%/*}"
printf '#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n' > "$output"
for n in 00000 00001 00002 00003 00004 00005; do
  printf 'synthetic segment' > "$dir/seg_$n.ts"
  printf '#EXTINF:2,\nseg_%s.ts\n' "$n" >> "$output"
done
printf '#EXT-X-ENDLIST\n' >> "$output"
exec tail -f /dev/null
`
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s.watcher.Config().Playback.FFmpegPath = ffmpeg
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	request, err := BoundTranscodeStartRequest(card)
	if err != nil {
		t.Fatal(err)
	}
	response := executorJSON(t, server.Client(), server.URL+"/transcode/start", request)
	if response.StatusCode != http.StatusAccepted {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("start %d: %s", response.StatusCode, data)
	}
	var receipt TranscodeStartResponse
	if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBoundTranscodeStartResponse(request, receipt); err != nil {
		t.Fatal(err)
	}
	response = executorJSON(t, server.Client(), server.URL+"/transcode/start", request)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate start %d", response.StatusCode)
	}
	s.mu.RLock()
	session := s.sessions[request.SessionID]
	s.mu.RUnlock()
	if session == nil || session.Opts().SessionID != card.SessionID || session.Opts().TranscodeTransportID != card.TranscodeTransportID {
		t.Fatal("worker lost playback versus transport identity")
	}
	calls, err := os.ReadFile(ffmpeg + ".calls")
	if err != nil || string(calls) != "launch\n" {
		t.Fatalf("launches %q: %v", calls, err)
	}
}

func TestExecutorPreparationPreservesTimestampAcrossWire(t *testing.T) {
	proposed := initialWorkerCard(t)
	proposed.OriginalStartedAt = time.Now()
	data, err := json.Marshal(proposed)
	if err != nil {
		t.Fatal(err)
	}
	var prepared playback.RecipeCard
	if err := json.Unmarshal(data, &prepared); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExecutorPreparation(proposed, prepared); err != nil {
		t.Fatalf("unchanged timestamp rejected after JSON round-trip: %v", err)
	}
	prepared.OriginalStartedAt = prepared.OriginalStartedAt.Add(time.Nanosecond)
	if err := ValidateExecutorPreparation(proposed, prepared); err == nil {
		t.Fatal("changed timestamp accepted")
	}
}
