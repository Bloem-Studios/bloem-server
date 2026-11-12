//go:build integration

package libraryingest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func strmTestRuntime(f nativeMovieScanFixture) *remotesource.Runtime {
	return &remotesource.Runtime{Store: remotesource.NewStore(f.pool), Guard: remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { return true }})}
}

// A `.strm` object in a storage source publishes as the movie its name says,
// probed from the URL it holds. The catalog keeps the opaque storage key; the
// URL and the object's revision live in bloem_remote_sources.
func TestNativeStrmStorageScanPublishesRemoteMovieDB(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeMovieScanFixture(t, binary, manifest, true, "movies")
	data, err := os.ReadFile(filepath.Join("..", "scanner", "testdata", "test.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "remote.mp4", time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	remoteURL := server.URL + "/films/remote.mp4?token=secret"
	if err := os.WriteFile(f.notify+".strm-url", []byte(remoteURL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.ReplaceConfiguration(t.Context(), f.installed.Source.Key, f.installed.Source.OwnerID, map[string]map[string]any{"source": {"mode": "movies", "notify": f.notify, "revision": "version:strm"}}); err != nil {
		t.Fatal(err)
	}
	rt := strmTestRuntime(f)
	f.scan.ConfigureRemoteSources(rt)

	result, err := f.scan.ScanStorageFolder(t.Context(), f.folder, storagesource.LocationSelection{Location: f.location})
	if err != nil {
		t.Fatal(err)
	}
	if result.ScanResult.New != 3 {
		t.Fatalf("new movie files = %d, want the two movies and the .strm", result.ScanResult.New)
	}
	var filePath, container, codec, title string
	if err := f.pool.QueryRow(t.Context(), `SELECT f.file_path,f.container,f.codec_video,i.title
		FROM media_files f JOIN media_items i ON i.content_id=f.content_id JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
		WHERE f.media_folder_id=$1 AND r.entry_id='03-shortcut'`, f.folder.ID).Scan(&filePath, &container, &codec, &title); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filePath, "bloem-storage:") || container != "mp4" || codec != "h264" || !strings.EqualFold(title, "remote") {
		t.Fatalf("published .strm path=%s container=%s codec=%s title=%s", filePath, container, codec, title)
	}
	source, ok, err := rt.Store.Get(t.Context(), filePath)
	if err != nil || !ok || source.URL != remoteURL || source.SourceRevision != "version:strm" || source.RemoteSize != int64(len(data)) {
		t.Fatalf("remote source = %+v, %v, %v", source, ok, err)
	}
	var leaked int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_folder_id=$1 AND (file_path LIKE '%token%' OR file_path LIKE 'http%')`, f.folder.ID).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("the URL leaked into a catalog path: %d %v", leaked, err)
	}
}

// A `.strm` object whose URL cannot be read is skipped; the rest of the page
// still publishes.
func TestNativeStrmStorageUnreadableURLSkipsOnlyThatEntryDB(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeMovieScanFixture(t, binary, manifest, true, "movies")
	f.scan.ConfigureRemoteSources(strmTestRuntime(f))
	result, err := f.scan.ScanStorageFolder(t.Context(), f.folder, storagesource.LocationSelection{Location: f.location})
	if err != nil {
		t.Fatal(err)
	}
	if result.ScanResult.New != 2 {
		t.Fatalf("new movie files = %d, want the two movies", result.ScanResult.New)
	}
	var sources int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_remote_sources s JOIN media_files f ON f.file_path=s.file_path WHERE f.media_folder_id=$1`, f.folder.ID).Scan(&sources); err != nil || sources != 0 {
		t.Fatalf("remote sources = %d, %v", sources, err)
	}
}

// A rescan of an unchanged `.strm` object (same object revision, same URL,
// same remote size and ETag) does not probe the remote media again: it costs
// one one-byte request. A changed ETag or object revision probes again.
func TestNativeStrmStorageRescanSkipsUnchangedRemoteDB(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeMovieScanFixture(t, binary, manifest, true, "movies")
	data, err := os.ReadFile(filepath.Join("..", "scanner", "testdata", "test.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	etag := `"v1"`
	var ranges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		w.Header().Set("ETag", etag)
		mu.Unlock()
		http.ServeContent(w, r, "remote.mp4", time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	take := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := ranges
		ranges = nil
		return out
	}
	if err := os.WriteFile(f.notify+".strm-url", []byte(server.URL+"/films/remote.mp4?token=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configure := func(revision string) {
		t.Helper()
		if _, err := f.registry.ReplaceConfiguration(t.Context(), f.installed.Source.Key, f.installed.Source.OwnerID, map[string]map[string]any{"source": {"mode": "movies", "notify": f.notify, "revision": revision}}); err != nil {
			t.Fatal(err)
		}
	}
	configure("version:strm")
	f.scan.ConfigureRemoteSources(strmTestRuntime(f))
	scan := func(pass string) {
		t.Helper()
		if _, err := f.scan.ScanStorageFolder(t.Context(), f.folder, storagesource.LocationSelection{Location: f.location}); err != nil {
			t.Fatalf("scan %s: %v", pass, err)
		}
	}
	codec := func() string {
		t.Helper()
		var codec string
		if err := f.pool.QueryRow(t.Context(), `SELECT COALESCE(f.codec_video,'') FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
			WHERE f.media_folder_id=$1 AND r.entry_id='03-shortcut' AND f.missing_since IS NULL`, f.folder.ID).Scan(&codec); err != nil {
			t.Fatal(err)
		}
		return codec
	}

	scan("first")
	if got := take(); !slices.Contains(got, "bytes=0-1048575") || codec() != "h264" {
		t.Fatalf("the first scan must probe the remote media: %q", got)
	}
	scan("unchanged")
	if got := take(); len(got) != 1 || got[0] != "bytes=0-0" {
		t.Fatalf("an unchanged .strm object asked the remote for %q, want one bytes=0-0", got)
	}
	if codec() != "h264" {
		t.Fatal("the reused probe was not published")
	}

	mu.Lock()
	etag = `"v2"`
	mu.Unlock()
	scan("remote changed")
	if got := take(); !slices.Contains(got, "bytes=0-1048575") {
		t.Fatalf("a changed remote was not probed again: %q", got)
	}

	configure("version:strm-2")
	scan("object changed")
	if got := take(); !slices.Contains(got, "bytes=0-1048575") || slices.Contains(got, "bytes=0-0") {
		t.Fatalf("a changed object must be read and probed again: %q", got)
	}
}
