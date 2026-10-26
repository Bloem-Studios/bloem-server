package nfo

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Bloem: a copy tool that restores a directory's modification time leaves
// only its change time to show the directory changed. A listing read in the
// same change-time tick as a change can miss a second change later in that
// tick, which leaves both times as they were, so the NFO listing cache keeps a
// listing only once the directory's change time has settled too.

func TestBloemListingNotCachedWhileChangeTimeIsFresh(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("change times are read on Linux and macOS only")
	}
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Film.mkv"), "video")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil { // a copy tool restores the time
		t.Fatalf("Chtimes() error = %v", err)
	}
	p := NewProvider()
	first, err := p.listDir(dir)
	if err != nil {
		t.Fatalf("listDir() error = %v", err)
	}
	if second, _ := p.listDir(dir); second == first {
		t.Fatal("a listing was cached while its directory's change time was fresh")
	}
}
