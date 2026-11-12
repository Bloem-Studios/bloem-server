package scanner

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/remotesource"
)

// remoteMediaServer serves testdata files over HTTP with range support, as a
// CDN behind a `.strm` file would.
func remoteMediaServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, filepath.Base(r.URL.Path), time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	return server
}

func loopbackGuard() *remotesource.Guard {
	return remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { return true }})
}

func requireFFprobe(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe required")
	}
	return path
}

func TestLibraryWalkTakesStrmFiles(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"Harbour Lights (2019)/Harbour Lights (2019).strm",
		"Show/Season 01/Show - S01E01.STRM",
		"Show/Season 01/Show - S01E02.mkv",
		"Show/Season 01/notes.txt",
	} {
		writeTestFile(t, filepath.Join(root, rel), "https://media.example.net/x.mkv\n")
	}
	for _, libraryType := range []string{"movies", "series"} {
		got := collectTestFilePaths(t, root, libraryType)
		assertFilePaths(t, got, root, []string{
			"Harbour Lights (2019)/Harbour Lights (2019).strm",
			"Show/Season 01/Show - S01E01.STRM",
			"Show/Season 01/Show - S01E02.mkv",
		})
	}
	if got := collectTestFilePaths(t, root, "audiobooks"); len(got) != 0 {
		t.Fatalf("an audiobook walk took %v", got)
	}
}

// ffprobe reads the remote MP4 through the loopback bridge and the guard, and
// the probe describes the remote media, not the `.strm` text.
func TestProbeRemoteReadsRemoteVideo(t *testing.T) {
	ffprobe := requireFFprobe(t)
	server := remoteMediaServer(t, map[string]string{"/films/harbour.mp4": "testdata/test.mp4", "/films/subs.mkv": "testdata/subtitles.mkv", "/films/no-extension": "testdata/subtitles.mkv"})
	s := &Scanner{ffprobePath: ffprobe}
	for path, container := range map[string]string{"/films/harbour.mp4": "mp4", "/films/subs.mkv": "mkv", "/films/no-extension": "mkv"} {
		result, err := s.ProbeRemote(t.Context(), loopbackGuard(), server.URL+path, nil)
		if err != nil {
			t.Fatalf("ProbeRemote(%s) = %v", path, err)
		}
		if result.Probe.Container != container || result.Probe.CodecVideo == "" || len(result.Probe.VideoTracks) == 0 || result.Size <= 0 {
			t.Fatalf("ProbeRemote(%s) = container %q codec %q size %d", path, result.Probe.Container, result.Probe.CodecVideo, result.Size)
		}
	}
	if _, err := s.ProbeRemote(t.Context(), loopbackGuard(), server.URL+"/missing.mkv", nil); err == nil {
		t.Fatal("a missing remote file must fail the probe")
	}
	authorityGone := errors.New("authority gone")
	if _, err := s.ProbeRemote(t.Context(), loopbackGuard(), server.URL+"/films/harbour.mp4", func(context.Context) error { return authorityGone }); !errors.Is(err, authorityGone) {
		t.Fatalf("a failed authority check must fail the probe, got %v", err)
	}
}

// By default the guard refuses the loopback server before ffprobe starts.
func TestProbeRemoteRefusesPrivateAddressesByDefault(t *testing.T) {
	ffprobe := requireFFprobe(t)
	server := remoteMediaServer(t, map[string]string{"/films/harbour.mp4": "testdata/test.mp4"})
	s := &Scanner{ffprobePath: ffprobe}
	guard := remotesource.NewGuard(remotesource.Options{})
	if _, err := s.ProbeRemote(t.Context(), guard, server.URL+"/films/harbour.mp4", nil); !errors.Is(err, remotesource.ErrBlockedAddress) {
		t.Fatalf("ProbeRemote = %v, want ErrBlockedAddress", err)
	}
	if _, err := s.ProbeRemote(t.Context(), guard, "http://169.254.169.254/latest/meta-data/", nil); !errors.Is(err, remotesource.ErrBlockedAddress) {
		t.Fatalf("metadata address = %v, want ErrBlockedAddress", err)
	}
}

func TestSniffRemoteDemuxer(t *testing.T) {
	cases := map[string]struct {
		body []byte
		want NativeProbeDemuxer
	}{
		"/a.bin":  {[]byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}, NativeProbeMatroska},
		"/b.bin":  {append([]byte{0, 0, 0, 0x20}, []byte("ftypisom0000")...), NativeProbeMOV},
		"/c.bin":  {append([]byte("RIFF\x00\x00\x00\x00AVI LIST"), make([]byte, 20)...), NativeProbeAVI},
		"/d.bin":  {tsPackets(188, 0), NativeProbeMPEGTS},
		"/e.bin":  {tsPackets(192, 4), NativeProbeMPEGTS},
		"/f.webm": {[]byte("not a known header but a known extension"), NativeProbeMatroska},
	}
	mux := http.NewServeMux()
	for path, c := range cases {
		body := c.body
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			http.ServeContent(w, r, "x", time.Time{}, bytes.NewReader(body))
		})
	}
	mux.HandleFunc("/g.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "x", time.Time{}, bytes.NewReader([]byte("hello")))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	guard := loopbackGuard()
	for path, c := range cases {
		f, err := remotesource.Open(t.Context(), guard.Client(), server.URL+path, remotesource.NameHint(server.URL+path))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := sniffRemoteDemuxer(f)
		_ = f.Close()
		if !ok || got != c.want {
			t.Errorf("sniff %s = %v, %v; want %v", path, got, ok, c.want)
		}
	}
	f, err := remotesource.Open(t.Context(), guard.Client(), server.URL+"/g.txt", "g.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, ok := sniffRemoteDemuxer(f); ok {
		t.Error("plain text must not pick a demuxer")
	}
}

func tsPackets(size, offset int) []byte {
	out := make([]byte, size*4)
	for i := 0; i < 4; i++ {
		out[offset+i*size] = 0x47
	}
	return out
}
