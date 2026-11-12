package scanner

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/remotesource"
)

// strmRemote serves one media file with a settable ETag and records the
// ranges asked for.
type strmRemote struct {
	mu     sync.Mutex
	etag   string
	data   []byte
	ranges []string
}

func (r *strmRemote) serve(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.ranges = append(r.ranges, req.Header.Get("Range"))
		etag, data := r.etag, r.data
		r.mu.Unlock()
		w.Header().Set("ETag", etag)
		http.ServeContent(w, req, "remote.mp4", time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	return server
}

func (r *strmRemote) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.ranges
	r.ranges = nil
	return out
}

func (r *strmRemote) setETag(etag string) {
	r.mu.Lock()
	r.etag = etag
	r.mu.Unlock()
}

// A rescan that has to look at a `.strm` file again (it was touched) but finds
// the same URL with the same remote size and ETag reuses the stored probe: one
// one-byte request, no media read. A changed ETag probes again.
func TestLocalStrmRescanSkipsUnchangedRemoteDB(t *testing.T) {
	ctx := t.Context()
	pool := newScannerTestPool(t)
	ffprobe := requireFFprobe(t)
	data, err := os.ReadFile("testdata/test.mp4")
	if err != nil {
		t.Fatal(err)
	}
	remote := &strmRemote{etag: `"v1"`, data: data}
	server := remote.serve(t)
	runtime := &remotesource.Runtime{Store: remotesource.NewStore(pool), Guard: loopbackGuard()}
	previous := strmRuntime
	strmRuntime = func(*Scanner) *remotesource.Runtime { return runtime }
	t.Cleanup(func() { strmRuntime = previous })

	root := t.TempDir()
	strmPath := filepath.Join(root, "Quiet Harbour (2018)", "Quiet Harbour (2018).strm")
	remoteURL := server.URL + "/films/quiet.mp4?token=one"
	writeTestFile(t, strmPath, remoteURL+"\n")
	var folderID int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, enabled) VALUES ('movies', 'Strm Rescan Test', true) RETURNING id`).Scan(&folderID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_folder_paths (media_folder_id, path) VALUES ($1, $2)`, folderID, root); err != nil {
		t.Fatalf("seed folder path: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM bloem_remote_sources WHERE file_path LIKE $1`, root+"%")
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM media_items WHERE content_id IN (SELECT content_id FROM media_files WHERE media_folder_id = $1)`, folderID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM media_folders WHERE id = $1`, folderID)
	})
	folder := &models.MediaFolder{ID: folderID, Type: "movies", Paths: []string{root}, Enabled: true}
	s := NewScanner(NewFileRepository(pool), ffprobe, nil, 1, false, 0)
	scan := func(pass string) {
		t.Helper()
		result, err := s.ScanFolder(ctx, folder)
		if err != nil || result.Errors != 0 {
			t.Fatalf("scan %s: %v (%d errors)", pass, err, result.Errors)
		}
	}
	codec := func() string {
		t.Helper()
		var codec string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(codec_video,'') FROM media_files WHERE media_folder_id=$1`, folderID).Scan(&codec); err != nil {
			t.Fatal(err)
		}
		return codec
	}
	touch := func(offset time.Duration) {
		t.Helper()
		at := time.Now().Add(offset)
		if err := os.Chtimes(strmPath, at, at); err != nil {
			t.Fatal(err)
		}
	}

	scan("first")
	if codec() == "" || !slices.Contains(remote.take(), "bytes=0-1048575") {
		t.Fatal("the first scan must probe the remote media")
	}
	src, ok, err := runtime.Store.Get(ctx, strmPath)
	if err != nil || !ok || src.Validators.ETag != `"v1"` || len(src.Probe) == 0 {
		t.Fatalf("recorded source = %+v, %v, %v", src, ok, err)
	}

	// Unchanged file: Silo's own size and time check skips it outright.
	scan("unchanged")
	if got := remote.take(); len(got) != 0 {
		t.Fatalf("an unchanged .strm file reached the remote: %q", got)
	}

	// Touched, same URL, same remote: one validator request, stored probe.
	touch(time.Hour)
	scan("touched")
	if got := remote.take(); len(got) != 1 || got[0] != "bytes=0-0" {
		t.Fatalf("a touched .strm with an unchanged remote asked for %q, want one bytes=0-0", got)
	}
	if codec() == "" {
		t.Fatal("the stored probe was not kept")
	}

	// The remote media changed (new ETag): it is probed again.
	remote.setETag(`"v2"`)
	touch(2 * time.Hour)
	scan("remote changed")
	if got := remote.take(); !slices.Contains(got, "bytes=0-1048575") {
		t.Fatalf("a changed remote was not probed again: %q", got)
	}
	if src, _, _ := runtime.Store.Get(ctx, strmPath); src.Validators.ETag != `"v2"` {
		t.Fatalf("validators after the reprobe = %+v", src.Validators)
	}
}
