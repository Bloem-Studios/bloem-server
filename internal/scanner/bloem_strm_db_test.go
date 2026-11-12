package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/remotesource"
)

// A local `.strm` file scans as the movie its name says, with the remote
// media's probe; its URL lives only in bloem_remote_sources. Editing the URL
// makes a new revision at the next scan, and a URL the guard refuses forgets
// the source so the item stops playing.
func TestLocalStrmScanRecordsRemoteSourceDB(t *testing.T) {
	ctx := t.Context()
	pool := newScannerTestPool(t)
	ffprobe := requireFFprobe(t)
	server := remoteMediaServer(t, map[string]string{"/films/harbor.mp4": "testdata/test.mp4", "/films/harbor-v2.mkv": "testdata/subtitles.mkv"})

	var allow atomic.Bool
	allow.Store(true)
	runtime := &remotesource.Runtime{Store: remotesource.NewStore(pool), Guard: remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { return allow.Load() }})}
	previous := strmRuntime
	strmRuntime = func(*Scanner) *remotesource.Runtime { return runtime }
	t.Cleanup(func() { strmRuntime = previous })

	root := t.TempDir()
	strmPath := filepath.Join(root, "Harbor Lights (2019)", "Harbor Lights (2019).strm")
	firstURL := server.URL + "/films/harbor.mp4?token=one"
	writeTestFile(t, strmPath, "\n"+firstURL+"\n")

	var folderID int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, enabled) VALUES ('movies', 'Strm Scan Test', true) RETURNING id`).Scan(&folderID); err != nil {
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
		if err != nil {
			t.Fatalf("scan %s: %v", pass, err)
		}
		if result.Errors != 0 {
			t.Fatalf("scan %s reported %d file errors", pass, result.Errors)
		}
	}
	type row struct {
		path, container, codec, title string
	}
	readRow := func() row {
		t.Helper()
		var r row
		if err := pool.QueryRow(ctx, `SELECT f.file_path, COALESCE(f.container,''), COALESCE(f.codec_video,''), COALESCE(f.base_title,'')
			FROM media_files f LEFT JOIN media_items i ON i.content_id=f.content_id WHERE f.media_folder_id=$1`, folderID).Scan(&r.path, &r.container, &r.codec, &r.title); err != nil {
			t.Fatalf("read media file: %v", err)
		}
		return r
	}

	scan("first")
	got := readRow()
	if got.path != strmPath || got.container != "mp4" || got.codec == "" {
		t.Fatalf("media file = %+v, want the .strm path with the remote MP4's probe", got)
	}
	if !strings.Contains(got.title, "Harbor Lights") {
		t.Fatalf("title = %q, want the name from the .strm file", got.title)
	}
	source, ok, err := runtime.Store.Get(ctx, strmPath)
	if err != nil || !ok || source.URL != firstURL || source.Revision != remotesource.Revision(firstURL) || source.RemoteSize <= 0 {
		t.Fatalf("remote source = %+v, %v, %v", source, ok, err)
	}
	var leaked int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_files WHERE media_folder_id=$1 AND (file_path LIKE '%token=%' OR file_path LIKE 'http%')`, folderID).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("the URL leaked into a catalog path: %d, %v", leaked, err)
	}

	// A new URL in the same file is a new revision with the new media's probe.
	secondURL := server.URL + "/films/harbor-v2.mkv"
	writeTestFile(t, strmPath, secondURL)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(strmPath, later, later); err != nil {
		t.Fatal(err)
	}
	scan("second")
	if got := readRow(); got.container != "mkv" {
		t.Fatalf("after the URL changed, container = %q, want mkv", got.container)
	}
	source, ok, _ = runtime.Store.Get(ctx, strmPath)
	if !ok || source.Revision != remotesource.Revision(secondURL) {
		t.Fatalf("after the URL changed, source = %+v", source)
	}

	// With private addresses refused, the loopback URL is refused at the next
	// scan and the source is forgotten.
	allow.Store(false)
	writeTestFile(t, strmPath, server.URL+"/films/harbor.mp4")
	evenLater := later.Add(time.Hour)
	if err := os.Chtimes(strmPath, evenLater, evenLater); err != nil {
		t.Fatal(err)
	}
	scan("third")
	if _, ok, _ := runtime.Store.Get(ctx, strmPath); ok {
		t.Fatal("a refused URL must forget the remote source")
	}
}
