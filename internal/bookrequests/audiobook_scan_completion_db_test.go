package bookrequests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// audioTools finds ffmpeg and ffprobe. A missing tool skips the test unless
// BLOEM_REQUIRE_FFMPEG=1, which CI sets once it installs them, so a broken
// install cannot pass by running nothing.
func audioTools(t *testing.T) (string, string) {
	t.Helper()
	missing := t.Skip
	if os.Getenv("BLOEM_REQUIRE_FFMPEG") == "1" {
		missing = t.Fatal
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		missing("ffmpeg unavailable; this test scans generated audio")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		missing("ffprobe unavailable; the scanner probes the generated audio")
	}
	return ffmpeg, ffprobe
}

// scannedAudio writes seconds of generated audio for the real scanner.
func scannedAudio(t *testing.T, ffmpeg, file string, seconds int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=330:sample_rate=44100:duration=%d", seconds), "-ac", "1"}
	if filepath.Ext(file) == ".m4b" {
		args = append(args, "-c:a", "aac", "-b:a", "32k", "-f", "mp4", file)
	} else {
		args = append(args, "-c:a", "libmp3lame", "-b:a", "32k", file)
	}
	if out, err := exec.CommandContext(t.Context(), ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("generate %s: %v: %s", file, err, out)
	}
}

// Completion evidence comes from what the real scanner writes for a verified
// download in its attempt-marker folder: a single M4B, a flat multi-part MP3
// book, and a multi-disc layout. The scanner treats each disc folder as its own
// book, so the multi-disc download must go to review rather than complete
// the request with one disc.
func TestAudiobookScannedDownloadCompletionDatabase(t *testing.T) {
	_, v, pool := databaseFixture(t)
	ffmpeg, ffprobe := audioTools(t)
	notices := &audiobookNoticeSpy{}
	fake := &audiobookFake{}
	s := NewService(pool, notices)
	s.SetAudiobookFulfillment(fake)
	admin := v
	admin.Admin = true
	integration, _ := audiobookIntegrationRow(t, pool, "Audiobooks")
	library := filepath.Join(t.TempDir(), "audiobooks")
	downloads := filepath.Join(library, "requests")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	folder := audiobookFolder(t, pool, v.OrganizationID, "Audio Books", "audiobooks", library)
	if _, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{downloads}); err != nil {
		t.Fatal(err)
	}
	fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/"+in.SubmissionID+"/"), nil
	}
	fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("completed", "verified", verified()), nil
	}
	layouts := []struct {
		title string
		files []string
	}{
		{"Single File Saga", []string{"Single File Saga.m4b"}},
		{"Flat Parts Saga", []string{"Part 01.mp3", "Part 02.mp3", "Part 03.mp3"}},
		{"Disc Saga", []string{"CD1/Track 01.mp3", "CD2/Track 01.mp3"}},
	}
	requests := make([]Request, len(layouts))
	for i, layout := range layouts {
		r, _, err := s.Create(t.Context(), v, CreateInput{Title: layout.title, Author: "Test Author"}, fmt.Sprintf("scan-completion-%08d", i))
		if err != nil {
			t.Fatal(err)
		}
		if r, err = s.Act(t.Context(), admin, r.ID, "approve", ActionInput{ExpectedRevision: r.Revision}); err != nil {
			t.Fatal(err)
		}
		requests[i] = r
	}
	reconcile := func() {
		t.Helper()
		bookSQL(t, pool, `UPDATE bloem_ebook_request_dispatches SET next_attempt_at=now()-interval '1 second'`)
		if _, err := s.ReconcileAudiobooks(t.Context(), 100); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	reconcile()
	for i, r := range requests {
		got, err := s.Get(t.Context(), admin, r.ID)
		if err != nil || got.Fulfilment != "awaiting_library" {
			t.Fatalf("%s before scan: %+v %v", layouts[i].title, got, err)
		}
		for _, file := range layouts[i].files {
			scannedAudio(t, ffmpeg, filepath.Join(downloads, r.Title+" ["+AudiobookMarker(r.ID, 1)+"]", file), 3)
		}
	}
	// A download in another attempt's marker folder is not evidence.
	scannedAudio(t, ffmpeg, filepath.Join(downloads, "Single File Saga ["+AudiobookMarker(requests[0].ID, 2)+"]", "other.m4b"), 3)
	sc := scanner.NewScanner(scanner.NewFileRepository(pool), ffprobe, nil, 2, false, 0)
	if err := sc.ScanAudiobookFolder(t.Context(), &models.MediaFolder{ID: folder, Type: "audiobooks", Name: "Audio Books", Paths: []string{library}, Enabled: true}, true); err != nil {
		t.Fatal(err)
	}
	reconcile()
	itemFiles := func(content string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE content_id=$1 AND missing_since IS NULL`, content).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i, r := range requests[:2] {
		got, err := s.Get(t.Context(), admin, r.ID)
		if err != nil || got.Status != "completed" || got.Fulfilment != "library" || got.LibraryContentID == "" {
			t.Fatalf("%s after scan: %+v %v", layouts[i].title, got, err)
		}
		if n := itemFiles(got.LibraryContentID); n != len(layouts[i].files) {
			t.Fatalf("%s completed with an item holding %d of %d files", layouts[i].title, n, len(layouts[i].files))
		}
	}
	disc, err := s.Get(t.Context(), admin, requests[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("multi-disc download: status=%s fulfilment=%s", disc.Status, disc.Fulfilment)
	completed := []string{requests[0].ID, requests[1].ID}
	switch {
	case disc.Status == "completed" && itemFiles(disc.LibraryContentID) == len(layouts[2].files):
		// A scanner that groups disc folders into one book may complete it.
		completed = append(completed, disc.ID)
	case disc.Status == "approved" && disc.Fulfilment == "review":
		acquisition, err := s.AudiobookAcquisition(t.Context(), admin, disc.ID)
		if err != nil || acquisition.ReviewKind != "verification" {
			t.Fatalf("multi-disc review: %+v %v", acquisition, err)
		}
		// Provisional, pending the multi-disc decision: accepting a download
		// that scanned as several books is refused today, so the admin can
		// only fulfill it manually. A plugin flag or disc-merging scanner
		// would change this.
		if _, err = s.AcceptAudiobookDownload(t.Context(), admin, disc.ID, ActionInput{ExpectedRevision: disc.Revision}); err == nil {
			t.Fatal("accept-download allowed for a download that scanned as several books")
		}
	default:
		t.Fatalf("multi-disc download must not complete with one disc: %+v", disc)
	}
	// Two notification passes: each completed request is announced once.
	for range 2 {
		if _, err = s.Reconcile(t.Context(), 100); err != nil {
			t.Fatal(err)
		}
	}
	notices.mu.Lock()
	fulfilled := slices.Sorted(slices.Values(notices.fulfilled))
	notices.mu.Unlock()
	slices.Sort(completed)
	if !slices.Equal(fulfilled, completed) {
		t.Fatalf("fulfilled notices: %v, want one each for %v", fulfilled, completed)
	}
}
