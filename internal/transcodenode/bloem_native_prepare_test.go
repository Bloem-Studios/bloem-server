package transcodenode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/downloadprepare"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
)

const preparationTransport = "6f1b7a64-3a60-4f0c-9c1a-3d8e0f5b2c11"

func TestNativePreparationTransport(t *testing.T) {
	for attempt, want := range map[string]string{
		"artifact-" + preparationTransport:         preparationTransport,
		"123456789-" + preparationTransport:        preparationTransport,
		preparationTransport:                       "",
		"artifact_" + preparationTransport:         "",
		"artifact-not-a-uuid-at-all-0000000000000": "",
	} {
		got, ok := NativePreparationTransport(attempt)
		if got != want || ok != (want != "") {
			t.Fatalf("%s: got %q %v", attempt, got, ok)
		}
	}
}

// A worker prepares a native download only under the grant sealed for the
// attempt's exact request, reading the granted source; it never opens the
// catalog reference.
func TestNativePreparationOnAWorkerReadsTheGrantedSource(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	source := filepath.Join(t.TempDir(), "movie.mp4")
	if out, err := exec.Command(bin, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=128x96:r=24", "-t", "2", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-movflags", "+faststart", source).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	opened := 0
	input := &playback.TranscodeInputSource{Open: func(ctx context.Context, opts playback.TranscodeOpts) (playback.TranscodeInput, error) {
		if opts.SessionID != preparationTransport || opts.InputPath != nativeExecutionTestLocation {
			return nil, errors.New("source opened outside its grant")
		}
		opened++
		lifetime, cancel := context.WithCancel(ctx)
		file, err := mediasource.OpenLocal(lifetime, source)
		if err != nil {
			cancel()
			return nil, err
		}
		return nativestorage.NewToolInput(lifetime, file, nativestorage.ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, MaxBytes: 64 << 20, ChunkBytes: 4096, MaxConcurrentReads: 2, Timeout: 30 * time.Second})
	}}
	server := newTestServer(t)
	server.inputPaths = NewNativeInputPathAuthorizer(nil)
	var resolved NativeExecutionRequest
	refuse := false
	wrapped := server.WrapNativeExecution(http.HandlerFunc(server.handleDownloadPrepare), NativeExecutionConfig{NodeID: 7, Resolve: func(_ context.Context, request NativeExecutionRequest) (*NativeExecutionGrant, error) {
		resolved = request
		if refuse {
			return nil, errors.New("no grant")
		}
		return &NativeExecutionGrant{WorkerID: 7, PlaybackSessionID: preparationTransport, TransportID: preparationTransport, InputPath: nativeExecutionTestLocation, Revision: "v1", InputFormat: "mov", InputSource: input, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}})
	request := downloadprepare.Request{ArtifactID: "artifact-" + preparationTransport, InputPath: nativeExecutionTestLocation, TargetCodecVideo: "h264", TargetCodecAudio: "aac",
		AudioTrackIndex: -1, TotalDuration: 2, LogSessionID: playback.DownloadPrepareLogSessionID("artifact")}
	post := func() *httptest.ResponseRecorder {
		r := jsonRequest(t, "/downloads/prepare", request)
		r.Header.Set("Authorization", "Bearer "+testSecret)
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		return w
	}

	refuse = true
	if w := post(); w.Code != http.StatusForbidden || opened != 0 {
		t.Fatalf("ungranted preparation: %d opened=%d", w.Code, opened)
	}
	if resolved.Prepare == nil || resolved.TransportID != preparationTransport || resolved.InputPath != nativeExecutionTestLocation {
		t.Fatalf("candidate = %+v", resolved)
	}
	refuse = false
	if w := post(); w.Code != http.StatusOK || opened != 1 {
		t.Fatalf("granted preparation: %d %s opened=%d", w.Code, w.Body.String(), opened)
	}
	data, err := os.ReadFile(filepath.Join(server.artifactRoot, request.ArtifactID+".mp4"))
	if err != nil || len(data) < 100 || string(data[4:8]) != "ftyp" {
		t.Fatalf("prepared artifact: %d bytes, %v", len(data), err)
	}

	request.ArtifactID, request.LogSessionID = "other-"+preparationTransport, ""
	if w := post(); w.Code == http.StatusOK || opened != 1 {
		t.Fatalf("preparation without its log session: %d opened=%d", w.Code, opened)
	}
}
