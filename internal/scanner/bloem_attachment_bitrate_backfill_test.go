package scanner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A native storage file's path is a catalog key and a `.strm` file's bytes
// are a URL: ffprobe can open neither, so the pass must not hand them to it.
func TestBloemAttachmentBitrateProbeSkipsNativeLocations(t *testing.T) {
	var probed []string
	b := &AttachmentBitrateBackfiller{probe: func(_ context.Context, _, filePath string) (containerBitrate, error) {
		probed = append(probed, filePath)
		return containerBitrate{kbps: 4047, size: 1000}, nil
	}}
	skipNativeAttachmentBitrateProbes(b)

	for _, path := range []string{"bloem-storage:0f3a", "/media/anime/show/episode.strm"} {
		got, err := b.probe(context.Background(), "ffprobe", path)
		if err != nil || got != otherTestsFile {
			t.Errorf("probe(%q) = %+v, %v; want the row left to its own scan", path, got, err)
		}
	}
	got, err := b.probe(context.Background(), "ffprobe", "/media/anime/show/episode.mkv")
	if err != nil || got != (containerBitrate{kbps: 4047, size: 1000}) {
		t.Errorf("probe(local) = %+v, %v; want ffprobe's answer", got, err)
	}
	if len(probed) != 1 || probed[0] != "/media/anime/show/episode.mkv" {
		t.Errorf("ffprobe read %q, want only the local file", probed)
	}
}

// Native files keep the rate their own scan stored, and the pass still
// finishes: it neither reads them nor lists them to read again.
func TestBloemAttachmentBitrateBackfillFinishesWithNativeFilesDB(t *testing.T) {
	pool := newScannerTestPool(t)
	ctx := context.Background()
	resetAttachmentBitrateBackfill(t, ctx, pool)
	folderID := seedDeadRootTestFolder(t, pool, "movies", fmt.Sprintf("attachment-bitrate-native-%d", time.Now().UnixNano()))

	ass := `[{"index":2,"codec":"ass"}]`
	local := t.TempDir() + "/fonts.mkv"
	native := fmt.Sprintf("bloem-storage:%064x", time.Now().UnixNano())
	localID := insertAttachmentBitrateRow(t, ctx, pool, folderID, local, attachmentBitrateRow{container: "mkv", subtitles: ass, stored: 8710})
	nativeID := insertAttachmentBitrateRow(t, ctx, pool, folderID, native, attachmentBitrateRow{container: "mkv", subtitles: ass, stored: 8710})

	var mu sync.Mutex
	reads := map[string]int{}
	backfiller := NewAttachmentBitrateBackfiller(NewFileRepository(pool), "ffprobe")
	// What ffprobe does with each: the catalog key is no file it can open.
	backfiller.probe = func(_ context.Context, _, filePath string) (containerBitrate, error) {
		mu.Lock()
		defer mu.Unlock()
		switch filePath {
		case local:
			reads["local"]++
			return containerBitrate{kbps: 4047, size: 1000}, nil
		case native:
			reads["native"]++
			return containerBitrate{}, errors.New("no such file or directory")
		}
		return otherTestsFile, nil
	}
	skipNativeAttachmentBitrateProbes(backfiller)

	result, err := backfiller.Run(ctx, nil)
	if err != nil || !result.Done || result.Pending != 0 {
		t.Fatalf("pass = %+v, %v; want done with nothing left to read again", result, err)
	}
	if reads["local"] != 1 || reads["native"] != 0 {
		t.Errorf("reads = %v, want the local file once and the native file never", reads)
	}
	if got := storedBitrate(t, ctx, pool, localID); got != 4047 {
		t.Errorf("local bitrate = %d, want 4047", got)
	}
	if got := storedBitrate(t, ctx, pool, nativeID); got != 8710 {
		t.Errorf("native bitrate = %d, want its stored 8710", got)
	}
}

// The server builds the pass with the native skip in place.
func TestNewBloemAttachmentBitrateBackfillerSkipsNativeLocations(t *testing.T) {
	if NewBloemAttachmentBitrateBackfiller(nil, "ffprobe") != nil {
		t.Fatal("want no backfiller without a database")
	}
	// The pool is never used: the native path answers before any query.
	b := NewBloemAttachmentBitrateBackfiller(&FileRepository{pool: &pgxpool.Pool{}}, "/nonexistent/ffprobe")
	got, err := b.probe(context.Background(), b.ffprobePath, "bloem-storage:0f3a")
	if err != nil || got != otherTestsFile {
		t.Fatalf("probe(native) = %+v, %v; want the row left to its own scan without running ffprobe", got, err)
	}
}
