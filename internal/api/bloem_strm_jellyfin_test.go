package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// strmCompatFixture is the Jellyfin native media host over a local `.strm`
// item: the compatibility session is the credential, and the bytes come from
// the recorded URL through the guard.
func strmCompatFixture(t *testing.T, data []byte, allowPrivate bool) (*compatServeFixture, *fakeStrmSources, string) {
	t.Helper()
	return strmCompatFixtureFor(t, newCompatServeFixture(t), data, allowPrivate)
}

func strmCompatFixtureFor(t *testing.T, f *compatServeFixture, data []byte, allowPrivate bool) (*compatServeFixture, *fakeStrmSources, string) {
	t.Helper()
	base := f.delegation.base
	base.file.FilePath = "/media/movies/Harbor Lights (2019)/Harbor Lights (2019).strm"
	base.file.Container = "mkv"
	server := strmMediaServer(t, map[string][]byte{"/films/harbor.mkv": data})
	remoteURL := server.URL + "/films/harbor.mkv?token=very-secret"
	sources := &fakeStrmSources{rows: map[string]remotesource.Source{}}
	sources.put(base.file.FilePath, remoteURL, "")
	strm := &strmPlayback{sources: sources, guard: remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { return allowPrivate }})}
	noStorage := func(context.Context, *models.MediaFile, nativestorage.PlaybackAuthority) (handlers.NativeVideoFile, error) {
		t.Error("a local .strm opened through a storage source")
		return nil, errors.New("no storage")
	}
	f.service.Files = compatFilesFixture{file: base.file}
	f.service.Open = func(ctx context.Context, file *models.MediaFile) (handlers.NativeVideoFile, error) {
		f.opens++
		authority, err := nativeVideoAuthority(ctx, file.ID, base.policy)
		if err != nil {
			return nil, err
		}
		return strm.open(ctx, file, authority, noStorage)
	}
	f.service.OpenDelegated = func(ctx context.Context, file *models.MediaFile, binding handlers.NativeVideoBinding) (handlers.NativeVideoFile, error) {
		authority, err := nativeVideoDelegatedAuthority(ctx, file, binding, base.policy)
		if err != nil {
			return nil, err
		}
		return strm.open(ctx, file, authority, noStorage)
	}
	f.host.install(f.service, base.policy.getSession, compatFilesFixture{file: base.file}, func() string { return compatTestNodeSecret })
	return f, sources, remoteURL
}

func strmCompatNoLeak(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for key, values := range w.Header() {
		for _, value := range values {
			if strings.Contains(value, "very-secret") || strings.Contains(value, "harbor.mkv") || strings.Contains(value, "127.0.0.1") {
				t.Fatalf("header %s names the URL: %q", key, value)
			}
		}
	}
	if bytes.Contains(w.Body.Bytes(), []byte("very-secret")) || bytes.Contains(w.Body.Bytes(), []byte("127.0.0.1")) {
		t.Fatal("the response body names the URL")
	}
}

// Jellyfin direct play of a `.strm` item serves the remote media's ranges
// under the compatibility session's authority.
func TestStrmJellyfinDirectPlay(t *testing.T) {
	data := strmBlob(2<<20 + 77)
	f, _, _ := strmCompatFixture(t, data, true)
	w := f.serve(t, f.request(http.MethodGet, "bytes=1048570-1048600"))
	body, _ := io.ReadAll(w.Body)
	if w.Code != http.StatusPartialContent || !bytes.Equal(body, data[1048570:1048601]) {
		t.Fatalf("range: %d, %d bytes", w.Code, len(body))
	}
	strmCompatNoLeak(t, w)
	w = f.serve(t, f.request(http.MethodHead, ""))
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("head: %d", w.Code)
	}
}

// Refusals answer 403 and never name the URL: a signed-out compatibility
// session, and an address the guard refuses.
func TestStrmJellyfinRefusalsAnswer403(t *testing.T) {
	data := strmBlob(4096)
	t.Run("signed-out", func(t *testing.T) {
		f, _, _ := strmCompatFixture(t, data, true)
		if w := f.serve(t, f.request(http.MethodGet, "bytes=0-1")); w.Code != http.StatusPartialContent {
			t.Fatalf("first request: %d", w.Code)
		}
		f.sessions.liveErr = errors.New("compat rows deleted")
		w := f.serve(t, f.request(http.MethodGet, "bytes=0-1"))
		if w.Code != http.StatusForbidden {
			t.Fatalf("revoked compatibility session = %d, want 403", w.Code)
		}
		strmCompatNoLeak(t, w)
	})
	t.Run("refused-address", func(t *testing.T) {
		f, _, _ := strmCompatFixture(t, data, false)
		w := f.serve(t, f.request(http.MethodGet, "bytes=0-1"))
		if w.Code != http.StatusForbidden {
			t.Fatalf("refused address = %d, want 403", w.Code)
		}
		strmCompatNoLeak(t, w)
	})
}

// Jellyfin remux and HLS transcode read the `.strm` item through the host's
// pinned input: ffmpeg sees the bridge, never the URL.
func TestStrmJellyfinRemuxAndTranscode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required")
	}
	media := filepath.Join(t.TempDir(), "remote.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=128x96:r=24", "-t", "3", "-c:v", "libx264", "-threads", "1", "-g", "24", "-pix_fmt", "yuv420p", media).CombinedOutput(); err != nil {
		t.Fatalf("synthetic media: %v %s", err, out)
	}
	data, err := os.ReadFile(media)
	if err != nil {
		t.Fatal(err)
	}
	f, _, _ := strmCompatFixture(t, data, true)
	path := f.delegation.base.file.FilePath

	w := httptest.NewRecorder()
	r := f.request(http.MethodGet, "")
	if err := playback.ServeRemuxWithOptions(w, r, path, "mp4", 0, false, -1, 0, playback.RemuxServeOptions{FFmpegPath: ffmpeg}); err != nil {
		t.Fatalf("remux: %v", err)
	}
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("ftyp")) {
		t.Fatalf("remux output %d/%d", w.Code, w.Body.Len())
	}

	out := t.TempDir()
	session, err := playback.StartTranscode(f.request(http.MethodGet, "").Context(), playback.TranscodeOpts{InputPath: path, SessionID: f.stream.PlaybackSessionID,
		OutputDir: out, FFmpegPath: ffmpeg, TargetCodecVideo: "h264", TargetCodecAudio: "copy", HWAccel: "none", TargetResolution: "720p", TargetBitrateKbps: 500, SegmentDuration: 1, TotalDuration: 3})
	if err != nil {
		t.Fatalf("transcode start: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	select {
	case <-session.Done():
	case <-time.After(60 * time.Second):
		t.Fatal("transcode did not finish")
	}
	if err := session.WaitError(); err != nil {
		t.Fatalf("transcode: %v", err)
	}
}

// remoteStartContext is the context of a Jellyfin stream request that
// serves the fixture's playback, as Silo's Jellyfin server starts a remote
// transcode in.
func (f *compatServeFixture) remoteStartContext() context.Context {
	ctx := playback.WithContextMediaHost(context.Background(), f.host)
	playback.NoteContextStream(ctx, f.stream)
	return ctx
}

func (f *compatServeFixture) remoteStart(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(transcodenode.TranscodeStartRequest{SessionID: f.stream.PlaybackSessionID, InputPath: f.delegation.base.file.FilePath, TargetCodecVideo: "h264", TargetCodecAudio: "aac", SegmentDuration: 6})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// Silo's transcode nodes read sources only through core source grants,
// which cannot name a native input, so a Jellyfin remote start for a stored
// movie or a local `.strm` item is refused before it is sent
// (jellycompat's remote start hook), and the route loop runs it on the API
// server. A library file in the same request is sent as before.
func TestNativeJellyfinRemoteStartStaysOnTheAPIServer(t *testing.T) {
	stored := newCompatServeFixtureFor(t, uuid.NewString())
	strm, _, _ := strmCompatFixtureFor(t, newCompatServeFixtureFor(t, uuid.NewString()), strmBlob(4096), true)
	for name, f := range map[string]*compatServeFixture{"stored movie": stored, "strm": strm} {
		if err := playback.RefuseContextRemoteStart(f.remoteStartContext(), f.remoteStart(t)); !errors.Is(err, playback.ErrContextInputStaysLocal) {
			t.Fatalf("%s: remote start = %v, want it kept on the API server", name, err)
		}
		library, _ := json.Marshal(transcodenode.TranscodeStartRequest{SessionID: f.stream.PlaybackSessionID, InputPath: "/media/Film (2019).mkv"})
		if err := playback.RefuseContextRemoteStart(f.remoteStartContext(), library); err != nil {
			t.Fatalf("%s: library file start refused: %v", name, err)
		}
	}
}
