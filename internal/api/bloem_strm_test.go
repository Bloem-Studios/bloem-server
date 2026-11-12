package api

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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type fakeStrmSources struct {
	mu   sync.Mutex
	rows map[string]remotesource.Source
}

func (f *fakeStrmSources) Get(_ context.Context, path string) (remotesource.Source, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[path]
	return row, ok, nil
}

func (f *fakeStrmSources) put(path, url, sourceRevision string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[path] = remotesource.Source{FilePath: path, URL: url, Revision: remotesource.Revision(url), SourceRevision: sourceRevision}
}

func strmBlob(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i*13 + i/509)
	}
	return data
}

func strmMediaServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "remote", time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	return server
}

type strmFixture struct {
	sources  *fakeStrmSources
	playback *strmPlayback
	file     *models.MediaFile
	service  *handlers.NativeVideoService
	session  *playback.Session
	allowed  *bool
}

func newStrmFixture(t *testing.T, allowPrivate bool) strmFixture {
	t.Helper()
	sources := &fakeStrmSources{rows: map[string]remotesource.Source{}}
	guard := remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { return allowPrivate }})
	s := &strmPlayback{sources: sources, guard: guard}
	file := &models.MediaFile{ID: 41, MediaFolderID: 7, ContentID: "movie-harbour-lights", FilePath: "/media/movies/Harbour Lights (2019)/Harbour Lights (2019).strm", Container: "mkv", CodecVideo: "h264", VideoTracks: []models.VideoTrack{{Codec: "h264"}}}
	allowed := true
	authority := nativestorage.PlaybackAuthority{AuthorizeFile: func(_ context.Context, requested *models.MediaFile) (*models.MediaFile, error) {
		if !allowed {
			return nil, catalog.ErrItemNotFound
		}
		copy := *file
		return &copy, nil
	}}
	service := &handlers.NativeVideoService{Files: nil, Bindings: handlers.NewNativeVideoBindingStore(nil)}
	service.Open = func(ctx context.Context, requested *models.MediaFile) (handlers.NativeVideoFile, error) {
		return s.open(ctx, requested, authority, func(context.Context, *models.MediaFile, nativestorage.PlaybackAuthority) (handlers.NativeVideoFile, error) {
			t.Fatal("a local .strm must never open through a storage source")
			return nil, nil
		})
	}
	session := &playback.Session{ID: uuid.NewString(), UserID: 3, ProfileID: "p", MediaFileID: file.ID, PlayMethod: playback.PlayDirect}
	return strmFixture{sources: sources, playback: s, file: file, service: service, session: session, allowed: &allowed}
}

func serveRange(t *testing.T, f strmFixture, rangeHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/stream/"+f.session.ID, nil)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	rec := httptest.NewRecorder()
	if err := f.service.Serve(rec, req, f.session, f.file); err != nil {
		t.Fatalf("Serve = %v", err)
	}
	return rec
}

// Direct play of a local `.strm` item: the client's Range requests are served
// from range reads of the remote URL, and nothing in the response names it.
func TestStrmDirectPlayProxiesRanges(t *testing.T) {
	data := strmBlob(3<<20 + 4321)
	server := strmMediaServer(t, map[string][]byte{"/films/harbour.mkv": data})
	f := newStrmFixture(t, true)
	remoteURL := server.URL + "/films/harbour.mkv?token=secret"
	f.sources.put(f.file.FilePath, remoteURL, "")
	if err := f.service.Bind(t.Context(), f.session, f.file); err != nil {
		t.Fatalf("Bind = %v", err)
	}

	rec := serveRange(t, f, "bytes=1048570-1048600")
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), data[1048570:1048601]) {
		t.Fatalf("range response = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 1048570-1048600/"+strconv.Itoa(len(data)) {
		t.Fatalf("Content-Range = %q", got)
	}
	rec = serveRange(t, f, "bytes=3145720-")
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), data[3145720:]) {
		t.Fatalf("open-ended range = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	rec = serveRange(t, f, "")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("whole body = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	for key, values := range rec.Header() {
		for _, value := range values {
			if strings.Contains(value, "token") || strings.Contains(value, "127.0.0.1") || strings.Contains(value, "harbour.mkv") {
				t.Fatalf("header %s leaks the remote address: %q", key, value)
			}
		}
	}
}

// A new URL after binding is a new revision: the bound session refuses it and
// a fresh attempt plays the new media.
func TestStrmURLChangeIsANewRevision(t *testing.T) {
	first, second := strmBlob(2<<20), strmBlob(5000)
	server := strmMediaServer(t, map[string][]byte{"/one.mkv": first, "/two.mkv": second})
	f := newStrmFixture(t, true)
	f.sources.put(f.file.FilePath, server.URL+"/one.mkv", "")
	if err := f.service.Bind(t.Context(), f.session, f.file); err != nil {
		t.Fatal(err)
	}
	opened, _, err := f.service.OpenBound(t.Context(), f.session, f.file)
	if err != nil {
		t.Fatal(err)
	}
	f.sources.put(f.file.FilePath, server.URL+"/two.mkv", "")
	if err := opened.Reauthorize(t.Context()); !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("Reauthorize after the URL changed = %v, want ErrReferenceConflict", err)
	}
	_ = opened.Close()
	if _, _, err := f.service.OpenBound(t.Context(), f.session, f.file); !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("OpenBound after the URL changed = %v, want ErrReferenceConflict", err)
	}
	fresh := *f.session
	fresh.ID = uuid.NewString()
	if err := f.service.Bind(t.Context(), &fresh, f.file); err != nil {
		t.Fatalf("a fresh attempt = %v", err)
	}
	f.session = &fresh
	if rec := serveRange(t, f, ""); !bytes.Equal(rec.Body.Bytes(), second) {
		t.Fatal("the fresh attempt did not play the new media")
	}
}

// Losing library access stops a bound `.strm` file at its next check.
func TestStrmReauthorizeChecksTheViewer(t *testing.T) {
	server := strmMediaServer(t, map[string][]byte{"/one.mkv": strmBlob(4096)})
	f := newStrmFixture(t, true)
	f.sources.put(f.file.FilePath, server.URL+"/one.mkv", "")
	opened, err := f.service.Open(t.Context(), f.file)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := opened.Reauthorize(t.Context()); err != nil {
		t.Fatal(err)
	}
	*f.allowed = false
	if err := opened.Reauthorize(t.Context()); !errors.Is(err, catalog.ErrItemNotFound) {
		t.Fatalf("Reauthorize without access = %v", err)
	}
}

// By default the guard refuses a URL on the server's own network, and a local
// `.strm` with no recorded source does not play.
func TestStrmOpenRefusesPrivateAddressesAndUnrecordedFiles(t *testing.T) {
	server := strmMediaServer(t, map[string][]byte{"/one.mkv": strmBlob(4096)})
	f := newStrmFixture(t, false)
	if _, err := f.service.Open(t.Context(), f.file); !errors.Is(err, catalog.ErrItemNotFound) {
		t.Fatalf("Open without a source = %v, want ErrItemNotFound", err)
	}
	f.sources.put(f.file.FilePath, server.URL+"/one.mkv", "")
	if _, err := f.service.Open(t.Context(), f.file); !errors.Is(err, remotesource.ErrBlockedAddress) {
		t.Fatalf("Open of a loopback URL = %v, want ErrBlockedAddress", err)
	}
	f.sources.put(f.file.FilePath, "http://169.254.169.254/latest/meta-data/", "")
	if _, err := f.service.Open(t.Context(), f.file); !errors.Is(err, remotesource.ErrBlockedAddress) {
		t.Fatalf("Open of the metadata address = %v, want ErrBlockedAddress", err)
	}
}

type fakeStoredStrm struct {
	identity nativestorage.PlaybackIdentity
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
	reauth   error
}

func (f *fakeStoredStrm) Read([]byte) (int, error)                 { return 0, io.EOF }
func (f *fakeStoredStrm) ReadAt([]byte, int64) (int, error)        { return 0, io.EOF }
func (f *fakeStoredStrm) Seek(int64, int) (int64, error)           { return 0, nil }
func (f *fakeStoredStrm) Close() error                             { f.closed = true; f.cancel(); return nil }
func (f *fakeStoredStrm) Context() context.Context                 { return f.ctx }
func (f *fakeStoredStrm) Cancel()                                  { f.cancel() }
func (f *fakeStoredStrm) Reauthorize(context.Context) error        { return f.reauth }
func (f *fakeStoredStrm) Err() error                               { return f.ctx.Err() }
func (f *fakeStoredStrm) Identity() nativestorage.PlaybackIdentity { return f.identity }
func (f *fakeStoredStrm) Info() mediasource.Info {
	return mediasource.Info{Name: "remote.strm", LogicalPath: "Movies/remote.strm", Revision: f.identity.Reference.Revision, Size: 60}
}

// A `.strm` object in a storage source opens through the source (its usual
// checks) and plays only the object revision the scan read the URL from.
func TestStrmStoredObjectNeedsTheScannedRevision(t *testing.T) {
	data := strmBlob(9000)
	server := strmMediaServer(t, map[string][]byte{"/remote.mkv": data})
	sources := &fakeStrmSources{rows: map[string]remotesource.Source{}}
	s := &strmPlayback{sources: sources, guard: remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { return true }})}
	file := &models.MediaFile{ID: 9, MediaFolderID: 2, ContentID: "remote", FilePath: "bloem-storage:abc", Container: "mkv", CodecVideo: "h264"}
	var base *fakeStoredStrm
	storage := func(ctx context.Context, _ *models.MediaFile, _ nativestorage.PlaybackAuthority) (handlers.NativeVideoFile, error) {
		lifetime, cancel := context.WithCancel(ctx)
		base = &fakeStoredStrm{identity: nativestorage.PlaybackIdentity{FileID: 9, FilePath: file.FilePath, Reference: storagesource.PersistedRef{Revision: "version:two"}}, ctx: lifetime, cancel: cancel}
		return base, nil
	}
	sources.put(file.FilePath, server.URL+"/remote.mkv", "version:one")
	if _, err := s.open(t.Context(), file, nativestorage.PlaybackAuthority{}, storage); !errors.Is(err, storagesource.ErrReferenceConflict) || !base.closed {
		t.Fatalf("open of a changed object = %v (base closed %v)", err, base.closed)
	}
	sources.put(file.FilePath, server.URL+"/remote.mkv", "version:two")
	opened, err := s.open(t.Context(), file, nativestorage.PlaybackAuthority{}, storage)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Info().Size != int64(len(data)) || opened.Info().Revision != "version:two" || opened.Identity() != base.identity {
		t.Fatalf("stored .strm info = %+v", opened.Info())
	}
	got := make([]byte, 100)
	if _, err := opened.ReadAt(got, 8000); err != nil || !bytes.Equal(got, data[8000:8100]) {
		t.Fatalf("ReadAt = %v", err)
	}
	base.reauth = storagesource.ErrSourceUnavailable
	if err := opened.Reauthorize(t.Context()); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatalf("Reauthorize must include the storage source's check, got %v", err)
	}
	_ = opened.Close()
	if !base.closed {
		t.Fatal("Close must release the storage object")
	}
	// Without a recorded source a storage file opens as itself.
	if _, err := s.open(t.Context(), &models.MediaFile{ID: 10, FilePath: "bloem-storage:def"}, nativestorage.PlaybackAuthority{}, storage); err != nil {
		t.Fatalf("plain storage open = %v", err)
	}
}

// Remux on the API server: ffmpeg reads the `.strm` item's remote media
// through the native input bridge (never the URL) and writes MP4.
func TestStrmRemuxRunsFFmpegThroughTheBridge(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required")
	}
	data, err := os.ReadFile(filepath.Join("..", "scanner", "testdata", "test.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.ServeContent(w, r, "remote.mp4", time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	f := newStrmFixture(t, true)
	f.file.Container = "mp4"
	f.session.PlayMethod = playback.PlayRemux
	f.sources.put(f.file.FilePath, server.URL+"/films/remote.mp4?token=secret", "")
	if err := f.service.Bind(t.Context(), f.session, f.file); err != nil {
		t.Fatal(err)
	}
	source, format, err := f.service.InputSource(t.Context(), f.session, f.file)
	if err != nil || format != "mov" {
		t.Fatalf("InputSource = %q, %v", format, err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v2/stream/"+f.session.ID, nil)
	opts := playback.TranscodeOpts{InputPath: f.file.FilePath, InputSource: source, InputFormat: format, SessionID: f.session.ID, FFmpegPath: ffmpeg}
	if err := playback.ServeSourceRemux(response, request, opts, "mp4", false, 0, playback.RemuxServeOptions{DVMode: playback.RemuxDVPreserveV3, FFmpegPath: ffmpeg}); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ftyp") || response.Body.Len() < 100 {
		t.Fatalf("remux output %d/%d", response.Code, response.Body.Len())
	}
	if requests.Load() == 0 {
		t.Fatal("the remote media was never read")
	}
}

// HLS transcode on the API server: StartTranscode encodes the `.strm` item's
// remote media, read through the same bridge, into segments.
func TestStrmHLSTranscodeReadsThroughTheBridge(t *testing.T) {
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
	server := strmMediaServer(t, map[string][]byte{"/remote.mkv": data})
	f := newStrmFixture(t, true)
	f.session.PlayMethod = playback.PlayTranscode
	f.sources.put(f.file.FilePath, server.URL+"/remote.mkv", "")
	if err := f.service.Bind(t.Context(), f.session, f.file); err != nil {
		t.Fatal(err)
	}
	source, format, err := f.service.InputSource(t.Context(), f.session, f.file)
	if err != nil || format != "matroska" {
		t.Fatalf("InputSource = %q, %v", format, err)
	}
	out := t.TempDir()
	session, err := playback.StartTranscode(t.Context(), playback.TranscodeOpts{InputPath: f.file.FilePath, InputSource: source, InputFormat: format, SessionID: f.session.ID,
		OutputDir: out, FFmpegPath: ffmpeg, TargetCodecVideo: "h264", TargetCodecAudio: "copy", HWAccel: "none", TargetResolution: "720p", TargetBitrateKbps: 500, SegmentDuration: 1, TotalDuration: 3})
	if err != nil {
		t.Fatal(err)
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
	segments, _ := filepath.Glob(filepath.Join(out, "*", "*.ts"))
	more, _ := filepath.Glob(filepath.Join(out, "*.ts"))
	m4s, _ := filepath.Glob(filepath.Join(out, "*", "*.m4s"))
	m4sTop, _ := filepath.Glob(filepath.Join(out, "*.m4s"))
	if len(segments)+len(more)+len(m4s)+len(m4sTop) == 0 {
		entries, _ := os.ReadDir(out)
		t.Fatalf("no HLS segments written: %v", entries)
	}
}
