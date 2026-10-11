package transcodenode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

func TestNativeExecutionRealWorkerStartAndLostWorkerReconstruction(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "source.mp4")
	if output, err := exec.Command(bin, "-v", "error", "-f", "lavfi", "-i", "color=c=green:s=128x96:r=24", "-t", "8", "-c:v", "libx264", "-threads", "1", "-g", "24", "-pix_fmt", "yuv420p", "-movflags", "+faststart", path).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	const identity, transport, sessionID, secret = "bloem-storage:synthetic-worker-pinned-source", "native-transport", "native-playback", "synthetic-worker-secret"
	var opens, releases atomic.Int32
	var deliveryOpens, deliveryCloses atomic.Int32
	source := &playback.TranscodeInputSource{Open: func(ctx context.Context, opts playback.TranscodeOpts) (playback.TranscodeInput, error) {
		if opts.InputPath != identity || opts.SessionID != transport {
			t.Errorf("worker source identity mismatch")
		}
		lifetime, cancel := context.WithCancel(ctx)
		file, err := mediasource.OpenLocal(lifetime, path)
		if err != nil {
			cancel()
			return nil, err
		}
		input, err := nativestorage.NewToolInput(lifetime, file, nativestorage.ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, ReleaseHost: func() { releases.Add(1) }, MaxBytes: 8 << 20, ChunkBytes: 4096, MaxConcurrentReads: 2, Timeout: 15 * time.Second})
		if err != nil {
			cancel()
			_ = file.Close()
			return nil, err
		}
		opens.Add(1)
		return input, nil
	}}
	newWorker := func() (*transcodenode.Server, http.Handler) {
		cfg := &config.Config{}
		cfg.Auth.JWTSecret = secret
		cfg.Playback.TranscodeDir, cfg.Playback.FFmpegPath, cfg.Playback.HWAccel = t.TempDir(), bin, "none"
		watcher := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
		watcher.SetConfigForTest(cfg)
		worker := transcodenode.NewServer(watcher, nodesessions.NewTracker(nil, "synthetic-node", "synthetic-node", "transcode"))
		worker.SetInputPathAuthorizer(transcodenode.NewNativeInputPathAuthorizer(nil))
		handler := worker.WrapNativeExecution(worker.Handler(), transcodenode.NativeExecutionConfig{NodeID: 7, Resolve: func(_ context.Context, candidate transcodenode.NativeExecutionRequest) (*transcodenode.NativeExecutionGrant, error) {
			if candidate.WorkerID != 7 || candidate.TransportID != transport || candidate.InputPath != identity {
				t.Error("wrong worker candidate")
			}
			return &transcodenode.NativeExecutionGrant{WorkerID: 7, PlaybackSessionID: sessionID, TransportID: transport, InputPath: identity, Revision: "synthetic-immutable-version", InputFormat: "mov", InputSource: source, ExpiresAt: time.Now().Add(time.Minute), OpenDelivery: func(ctx context.Context) (transcodenode.NativeDeliveryAuthority, error) {
				deliveryOpens.Add(1)
				return openIntegrationNativeDelivery(ctx, path, &deliveryCloses)
			}}, nil
		}})
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = worker.Shutdown(ctx)
		})
		return worker, handler
	}
	worker, handler := newWorker()
	body, err := json.Marshal(transcodenode.TranscodeStartRequest{SessionID: transport, InputPath: identity, TargetCodecVideo: "h264", TargetCodecAudio: "aac", HWAccel: "none", SegmentDuration: 1, TotalDuration: 8, AudioTrackIndex: -1, SubtitleTrackIndex: -1, RequireReady: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/transcode/start", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("worker start status %d: %s", response.Code, response.Body.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := worker.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 1 || releases.Load() != 1 {
		t.Fatalf("lost worker leaked input: %d/%d", opens.Load(), releases.Load())
	}
	_, successor := newWorker()
	card := playback.NewRecipeCard(1, "profile", 1, "", playback.TranscodeOpts{SessionID: sessionID, TranscodeTransportID: transport, InputPath: identity, TargetCodecVideo: "h264", TargetCodecAudio: "aac", SegmentDuration: 1, TotalDuration: 8, AudioTrackIndex: -1, SubtitleTrackIndex: -1})
	token, err := streamtoken.Sign(card.ToClaims(), secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/transcode/"+transport+"/master.m3u8", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("X-Silo-Stream-Token", token)
	response = httptest.NewRecorder()
	successor.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("#EXTM3U")) {
		t.Fatalf("worker reconstruct status %d: %s", response.Code, response.Body.String())
	}
	if opens.Load() != 2 {
		t.Fatal("reconstruction reused old source handle")
	}
	if deliveryOpens.Load() != 1 || deliveryCloses.Load() != 1 {
		t.Fatal("generated manifest did not retain/join source authority")
	}
	segment := ""
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if strings.HasPrefix(line, "segment/") {
			segment = strings.TrimSpace(line)
			break
		}
	}
	if segment == "" {
		t.Fatalf("generated manifest omitted media segment: %q", response.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request = httptest.NewRequest(method, "/transcode/"+transport+"/"+segment, nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		request.Header.Set("X-Silo-Stream-Token", token)
		response = httptest.NewRecorder()
		successor.ServeHTTP(response, request)
		wantStatus := http.StatusOK
		if method == http.MethodHead {
			// The upstream HLS route accepts GET only. Native delivery must
			// authorize and join the owner without widening that route contract.
			wantStatus = http.StatusMethodNotAllowed
		}
		if response.Code != wantStatus || method == http.MethodGet && response.Body.Len() == 0 || method == http.MethodHead && response.Body.Len() != 0 {
			t.Fatalf("generated segment %s: %d bytes=%d", method, response.Code, response.Body.Len())
		}
	}
	if deliveryOpens.Load() != 3 || deliveryCloses.Load() != 3 || opens.Load() != 2 {
		t.Fatalf("artifact delivery leaked/reopened tools: owner=%d/%d inputs=%d", deliveryOpens.Load(), deliveryCloses.Load(), opens.Load())
	}
}
