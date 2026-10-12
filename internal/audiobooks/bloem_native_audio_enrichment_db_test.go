package audiobooks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type nativeCoverSpy struct{ calls int }

func (s *nativeCoverSpy) CacheAudiobookCover(context.Context, []byte, string) (string, string, error) {
	s.calls++
	return "unexpected", "", nil
}

func TestNativeAudioEnrichmentCoverFallbackReadsNothing(t *testing.T) {
	pool := newABSIdentityDatabase(t)
	const address = "bloem-storage:native-audio-cover"
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_folders(id,name,type,enabled) VALUES(7,'Audio fixture','audiobooks',true);
 INSERT INTO media_items(content_id,type,title) VALUES('native-cover','audiobook','Native cover');
 INSERT INTO media_files(content_id,media_folder_id,file_path,file_size) VALUES('native-cover',7,'bloem-storage:native-audio-cover',0)`); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Chdir(root)
	// An on-disk name collision must remain unread: ffmpeg interprets the reserved
	// prefix as a protocol rather than a physical filename.
	if err := os.WriteFile(address, []byte("unrelated local bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(root, "ffmpeg-wrapper")
	log := filepath.Join(root, "stderr")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec '"+ffmpeg+"' \"$@\" 2>'"+log+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	spy := &nativeCoverSpy{}
	e := &Enricher{pool: pool, ffmpegPath: wrapper, imageCacher: spy}
	if err := e.applyLocalCoverFallback(t.Context(), "native-cover"); err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stderr), "Protocol not found") || !strings.Contains(string(stderr), address) {
		t.Fatalf("ffmpeg did not reject protocol: %s", stderr)
	}
	var poster string
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(poster_path,'') FROM media_items WHERE content_id='native-cover'`).Scan(&poster); err != nil {
		t.Fatal(err)
	}
	if poster != "" || spy.calls != 0 {
		t.Fatalf("poster=%q cache calls=%d", poster, spy.calls)
	}
}
