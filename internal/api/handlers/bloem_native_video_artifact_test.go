package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/redis/go-redis/v9"
)

type nativeArtifactDenyIO struct {
	mu     sync.Mutex
	denied bool
	err    error
}

func (*nativeArtifactDenyIO) DialHook(next redis.DialHook) redis.DialHook { return next }
func (*nativeArtifactDenyIO) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (d *nativeArtifactDenyIO) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.err != nil {
			cmd.SetErr(d.err)
			return d.err
		}
		switch cmd.Name() {
		case "set":
			d.denied = true
			cmd.(*redis.StatusCmd).SetVal("OK")
		case "get":
			if !d.denied {
				cmd.SetErr(redis.Nil)
				return redis.Nil
			}
			cmd.(*redis.StringCmd).SetVal("1")
		case "exists":
			if d.denied {
				cmd.(*redis.IntCmd).SetVal(1)
			} else {
				cmd.(*redis.IntCmd).SetVal(0)
			}
		default:
			return errors.New("unexpected native artifact deny command")
		}
		return nil
	}
}

type nativeArtifactSource struct {
	NativeVideoFile
	ctx           context.Context
	sourceRevoked *atomic.Bool
	cancelled     *atomic.Bool
}

func (s *nativeArtifactSource) Context() context.Context { return s.ctx }
func (s *nativeArtifactSource) Cancel()                  { s.cancelled.Store(true); s.NativeVideoFile.Cancel() }
func (s *nativeArtifactSource) Reauthorize(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.sourceRevoked.Load() {
		return errors.New("source revoked")
	}
	return s.NativeVideoFile.Reauthorize(ctx)
}

type nativeArtifactWriter struct {
	*httptest.ResponseRecorder
	afterFirstWrite func()
	once            sync.Once
}

func (w *nativeArtifactWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if n > 0 {
		w.once.Do(func() {
			if w.afterFirstWrite != nil {
				w.afterFirstWrite()
			}
		})
	}
	return n, err
}

func nativeArtifactFixture(t *testing.T) (*PlaybackHandler, *playback.Session, *playback.TranscodeSession, *playback.SegmentLease, []byte, *atomic.Bool, *atomic.Bool, *atomic.Bool, *nativeArtifactDenyIO, *redis.Client, *int) {
	t.Helper()
	service, file, session, _, closed := nativeVideoFixture(t)
	if err := service.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	actorRevoked, sourceRevoked, cancelled := new(atomic.Bool), new(atomic.Bool), new(atomic.Bool)
	dio := &nativeArtifactDenyIO{}
	client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	client.AddHook(dio)
	t.Cleanup(func() { _ = client.Close() })
	service.Deny = playback.NewStreamDeny(client)
	if service.Deny.Denied(t.Context(), session.ID) {
		t.Fatal("initial deny fixture")
	}
	service.AuthorizeBound = func(ctx context.Context, binding NativeVideoBinding) error {
		if actorRevoked.Load() {
			return errors.New("actor revoked")
		}
		if binding.SessionID != session.ID || binding.Identity.FileID != file.ID {
			return errors.New("wrong artifact binding")
		}
		if _, err := service.Sessions.GetSession(session.ID); err != nil {
			return err
		}
		count, err := client.Exists(ctx, playback.StreamDenyKey(session.ID)).Result()
		if err != nil {
			return err
		}
		if count != 0 {
			return errPlaybackSessionEnded
		}
		return nil
	}
	open := service.Open
	service.Open = func(ctx context.Context, file *models.MediaFile) (NativeVideoFile, error) {
		original, err := open(ctx, file)
		if err != nil {
			return nil, err
		}
		return &nativeArtifactSource{NativeVideoFile: original, ctx: ctx, sourceRevoked: sourceRevoked, cancelled: cancelled}, nil
	}
	h := NewPlaybackHandler(service.Sessions, service.Files)
	h.NativeVideo = service
	dir := t.TempDir()
	data := bytes.Repeat([]byte("0123456789abcdef"), 8192)
	if err := os.WriteFile(filepath.Join(dir, "seg_00005.ts"), data, 0600); err != nil {
		t.Fatal(err)
	}
	tc := playback.NewTranscodeSessionForTest(dir)
	lease, err := tc.OpenSegment("seg_00005.ts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	return h, session, tc, lease, data, actorRevoked, sourceRevoked, cancelled, dio, client, closed
}

func TestNativeVideoGeneratedSegmentStopsAfterFirstChunk(t *testing.T) {
	for _, kind := range []string{"local-stop", "durable-deny", "actor-revoked", "source-revoked", "redis-error"} {
		t.Run(kind, func(t *testing.T) {
			h, session, tc, lease, data, actorRevoked, sourceRevoked, cancelled, dio, client, closed := nativeArtifactFixture(t)
			w := &nativeArtifactWriter{ResponseRecorder: httptest.NewRecorder()}
			w.afterFirstWrite = func() {
				switch kind {
				case "local-stop":
					if err := h.sessionMgr.StopSession(session.ID); err != nil {
						t.Fatal(err)
					}
				case "durable-deny":
					playback.NewStreamDeny(client).Deny(t.Context(), session.ID)
				case "actor-revoked":
					actorRevoked.Store(true)
				case "source-revoked":
					sourceRevoked.Store(true)
				case "redis-error":
					dio.mu.Lock()
					dio.err = errors.New("Redis unavailable")
					dio.mu.Unlock()
				}
			}
			var aborted any
			handled := false
			func() {
				defer func() { aborted = recover() }()
				handled = h.serveNativeVideoSegment(httpstream.NewRollingDeadlineWriter(w), playbackTestRequest(http.MethodGet, "/segment/seg_00005.ts", nil, map[string]string{"name": "seg_00005.ts"}), session, "bloem-storage:movie", lease, tc)
			}()
			//nolint:errorlint // net/http requires this exact panic value to suppress abort logging.
			if aborted != http.ErrAbortHandler {
				t.Fatalf("native segment did not abort: handled=%v panic=%v bytes=%d", handled, aborted, w.Body.Len())
			}
			if w.Body.Len() == 0 || w.Body.Len() >= len(data) || !bytes.Equal(w.Body.Bytes(), data[:w.Body.Len()]) {
				t.Fatalf("bad truncated segment: %d/%d", w.Body.Len(), len(data))
			}
			if !cancelled.Load() || *closed != 2 {
				t.Fatalf("source authority not joined: cancelled=%v closes=%d", cancelled.Load(), *closed)
			}
			if tc.LastRequestedSegment() >= 5 {
				t.Fatal("aborted generation reported downloaded")
			}
		})
	}
}

func TestNativeVideoGeneratedSegmentPreservesHTTPAndGeneration(t *testing.T) {
	for _, kind := range []string{"full", "range", "head", "not-modified", "filesystem"} {
		t.Run(kind, func(t *testing.T) {
			h, session, tc, lease, data, _, _, _, _, _, closed := nativeArtifactFixture(t)
			method := http.MethodGet
			if kind == "head" {
				method = http.MethodHead
			}
			r := playbackTestRequest(method, "/segment/seg_00005.ts", nil, map[string]string{"name": "seg_00005.ts"})
			if kind == "range" {
				r.Header.Set("Range", "bytes=3-18")
			}
			if kind == "not-modified" {
				r.Header.Set("If-Modified-Since", lease.Info.ModTime().UTC().Format(http.TimeFormat))
			}
			inputPath := "bloem-storage:movie"
			if kind == "filesystem" {
				inputPath = "/media/movie.mp4"
			}
			w := httptest.NewRecorder()
			handled := h.serveNativeVideoSegment(httpstream.NewRollingDeadlineWriter(w), r, session, inputPath, lease, tc)
			if kind == "filesystem" {
				if handled || w.Body.Len() != 0 || *closed != 1 {
					t.Fatal("filesystem response entered native guard")
				}
				http.ServeContent(w, r, lease.Info.Name(), lease.Info.ModTime(), lease.File)
			} else if !handled {
				t.Fatal("native segment fell through to filesystem serving")
			}
			want, status := data, http.StatusOK
			if kind == "range" {
				want, status = data[3:19], http.StatusPartialContent
			}
			if kind == "head" {
				want = nil
			}
			if kind == "not-modified" {
				want, status = nil, http.StatusNotModified
			}
			if w.Code != status || !bytes.Equal(w.Body.Bytes(), want) {
				t.Fatalf("status=%d bytes=%d want=%d/%d", w.Code, w.Body.Len(), status, len(want))
			}
			downloaded := tc.LastRequestedSegment()
			if kind == "full" && downloaded != 5 {
				t.Fatalf("full response lost generation report: %d", downloaded)
			}
			if kind != "full" && downloaded >= 5 {
				t.Fatalf("partial/no-body response reported downloaded: %d", downloaded)
			}
			if _, err := lease.File.Seek(0, io.SeekStart); err != nil {
				t.Fatal("helper closed owner segment lease")
			}
			if lease.Generation != 0 {
				t.Fatalf("generation changed: %d", lease.Generation)
			}
		})
	}
}

func TestNativeVideoGeneratedSegmentRefusesChangedCatalogBeforeHeaders(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			h, session, tc, lease, _, _, _, _, _, _, _ := nativeArtifactFixture(t)
			file, err := h.NativeVideo.Files.GetByID(t.Context(), session.MediaFileID)
			if err != nil {
				t.Fatal(err)
			}
			file.FilePath = "/media/replacement.mp4"
			r := playbackTestRequest(method, "/segment/seg_00005.ts", nil, map[string]string{"name": "seg_00005.ts"})
			r.Header.Set("If-Modified-Since", lease.Info.ModTime().UTC().Format(http.TimeFormat))
			w := httptest.NewRecorder()
			if !h.serveNativeVideoSegment(httpstream.NewRollingDeadlineWriter(w), r, session, "bloem-storage:movie", lease, tc) || w.Code == http.StatusNotModified || w.Code == http.StatusOK || bytes.Contains(w.Body.Bytes(), []byte("0123456789abcdef")) {
				t.Fatalf("catalog mutation bypassed native guard: status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

type nativeArtifactReadHook struct {
	io.ReadSeeker
	after func()
}

func (r *nativeArtifactReadHook) Read(p []byte) (int, error) {
	n, err := r.ReadSeeker.Read(p)
	r.after()
	return n, err
}
func (r *nativeArtifactReadHook) Seek(offset int64, whence int) (int64, error) {
	pos, err := r.ReadSeeker.Seek(offset, whence)
	r.after()
	return pos, err
}

func TestNativeVideoGeneratedSegmentDiscardsRevokedFileRead(t *testing.T) {
	for _, seek := range []bool{false, true} {
		t.Run(map[bool]string{false: "read", true: "seek"}[seek], func(t *testing.T) {
			h, session, _, lease, _, actorRevoked, _, cancelled, _, _, closed := nativeArtifactFixture(t)
			file, err := h.NativeVideo.Files.GetByID(t.Context(), session.MediaFileID)
			if err != nil {
				t.Fatal(err)
			}
			authority, _, err := h.NativeVideo.OpenBound(t.Context(), session, file)
			if err != nil {
				t.Fatal(err)
			}
			stream := &nativeVideoArtifactReader{input: &nativeArtifactReadHook{ReadSeeker: lease.File, after: func() { actorRevoked.Store(true) }}, authority: authority, ctx: t.Context()}
			buffer := bytes.Repeat([]byte{0x7f}, 32)
			if seek {
				pos, err := stream.Seek(3, io.SeekStart)
				if pos != 0 || err == nil {
					t.Fatalf("revoked seek returned position=%d err=%v", pos, err)
				}
			} else {
				n, err := stream.Read(buffer)
				if n != 0 || err == nil || !bytes.Equal(buffer, make([]byte, len(buffer))) {
					t.Fatalf("revoked artifact read leaked n=%d err=%v data=%x", n, err, buffer)
				}
			}
			if !cancelled.Load() {
				t.Fatal("artifact denial did not cancel source authority")
			}
			actorRevoked.Store(false)
			if n, err := stream.Read(buffer); n != 0 || err == nil {
				t.Fatalf("artifact denial was not sticky: %d/%v", n, err)
			}
			if err := authority.Close(); err != nil {
				t.Fatal(err)
			}
			if *closed != 2 {
				t.Fatalf("authority was not joined: %d", *closed)
			}
		})
	}
}

func TestNativeVideoGeneratedSegmentHandlerUsesExecutingSourceAuthority(t *testing.T) {
	a := newNativeAcceptance(t, "mp4")
	decision := a.start(t, "hls", true)
	tc := a.h.tm.GetTranscodeSession(decision.SessionID)
	waitNativeAcceptance(t, tc)
	t.Cleanup(func() { _ = tc.Close() })
	if tc.Opts().InputPath != a.file.FilePath {
		t.Fatal("fixture did not create an actual native execution")
	}
	name := "seg_00000.m4s"
	data := bytes.Repeat([]byte("native-generated-artifact"), 8192)
	if err := os.WriteFile(filepath.Join(tc.Opts().OutputDir, name), data, 0600); err != nil {
		t.Fatal(err)
	}
	w := &nativeArtifactWriter{ResponseRecorder: httptest.NewRecorder(), afterFirstWrite: func() { a.p.revoked.Store(true) }}
	var aborted any
	func() {
		defer func() { aborted = recover() }()
		a.h.HandleGetTranscodeSegment(w, playbackTestRequest(http.MethodGet, "/transcode/"+decision.SessionID+"/"+name, nil, map[string]string{"session_id": decision.SessionID, "name": name}))
	}()
	//nolint:errorlint // net/http requires this exact panic value to suppress abort logging.
	if aborted != http.ErrAbortHandler || w.Body.Len() == 0 || w.Body.Len() >= len(data) || !bytes.Equal(w.Body.Bytes(), data[:w.Body.Len()]) {
		t.Fatalf("handler bypassed artifact source authority: panic=%v bytes=%d/%d status=%d", aborted, w.Body.Len(), len(data), w.Code)
	}
	if a.p.active.Load() != 0 {
		t.Fatalf("handler returned before joining source admission: %d", a.p.active.Load())
	}
}
