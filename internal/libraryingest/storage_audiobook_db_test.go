//go:build integration

package libraryingest

import (
	"maps"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func nativeAudioLaneFixture(t *testing.T, mode string) nativeMovieScanFixture {
	t.Helper()
	binary, manifest := nativeMovieScanArtifact(t)
	// The fixture installs and attaches its source before runtime startup. Supply
	// the finite audio bytes at the configured path before the first Describe.
	f := newNativeMovieScanFixture(t, binary, manifest, true, "movies")
	extension, codec := "mp3", "libmp3lame"
	if mode == "audio-m4b" {
		extension, codec = "m4b", "aac"
	}
	command := exec.CommandContext(t.Context(), "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2.75", "-c:a", codec, "-y", f.notify+"."+extension)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("audio fixture: %v %s", err, out)
	}
	// Install's Describe is performed before the scan. Update through the real
	// registry so the provider generation and configuration are both pinned.
	_, err := f.registry.ReplaceConfiguration(t.Context(), f.installed.Source.Key, f.installed.Source.OwnerID, map[string]map[string]any{"source": {"mode": mode, "notify": f.notify, "revision": "version:one"}})
	if err != nil {
		t.Fatal(err)
	}
	f.folder.Type = "audiobooks"
	if _, err := f.pool.Exec(t.Context(), `UPDATE media_folders SET type='audiobooks' WHERE id=$1`, f.folder.ID); err != nil {
		t.Fatal(err)
	}
	reader, err := nativestorage.NewScanReader(f.pool, f.scan.host)
	if err != nil {
		t.Fatal(err)
	}
	cfg := scanner.NativeProbeConfig{Input: nativestorage.ToolInputConfig{MaxBytes: 128 << 20, ChunkBytes: 128 << 10, MaxConcurrentReads: 2, Timeout: 60 * time.Second}, MaxJSONBytes: 4 << 20, MaxStderrBytes: 64 << 10}
	gate := new(atomic.Bool)
	gate.Store(true)
	if err := f.scan.ConfigureNativeAudio(true, reader, cfg, gate); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestNativeAudioScanProbesThroughPinnedReaderDB(t *testing.T) {
	for _, mode := range []string{"audio", "audio-m4b", "audio-stat-conflict", "audio-read-revision-change"} {
		t.Run(mode, func(t *testing.T) {
			f := nativeAudioLaneFixture(t, mode)
			result, err := f.scan.ScanStorageFolder(t.Context(), f.folder, storagesource.LocationSelection{Location: f.location})
			if mode == "audio-stat-conflict" || mode == "audio-read-revision-change" {
				// A revision that changes during the read only rejects the book.
				if (err == nil) != (mode == "audio-read-revision-change") {
					t.Fatalf("revision conflict: %v", err)
				}
				var count int
				if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_folder_id=$1`, f.folder.ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("conflicting publication=%d err=%v", count, err)
				}
				return
			}
			if err != nil || result.ScanResult.New != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			audit, err := os.ReadFile(f.notify + ".io")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(audit), "stat track") || !strings.Contains(string(audit), "read track") {
				t.Fatalf("pinned I/O missing: %s", audit)
			}
			var count int
			if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE f.media_folder_id=$1 AND f.duration>=2 AND r.revision='version:one'`, f.folder.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("publication=%d err=%v", count, err)
			}
		})
	}
}

func TestNativeAudioScanRequiresDeclaredAudioKindDB(t *testing.T) {
	for _, mode := range []string{"audio-no-kind", "audio-no-directory", "gate", "ready"} {
		t.Run(mode, func(t *testing.T) {
			f := nativeAudioLaneFixture(t, strings.ReplaceAll(strings.ReplaceAll(mode, "gate", "audio"), "ready", "audio"))
			if mode == "gate" {
				f.scan.audioGate.Store(false)
			}
			if mode == "ready" {
				f.scan.nativeAudioReady = false
			}
			if _, err := f.scan.ScanStorageFolder(t.Context(), f.folder, storagesource.LocationSelection{Location: f.location}); err == nil {
				t.Fatal("undeclared or held audio source admitted")
			}
			var count int
			if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_scan_runs WHERE location_id=$1`, f.location.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("lease before admission=%d err=%v", count, err)
			}
		})
	}
}

func TestNativeAudioScanListsAudioDirectoriesInFullDB(t *testing.T) {
	f := nativeAudioLaneFixture(t, "audio-token")
	for i := 0; i < 2; i++ {
		if _, err := f.scan.ScanStorageFolder(t.Context(), f.folder, storagesource.LocationSelection{Location: f.location}); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_change_tokens WHERE location_id=$1 AND token<>''`, f.location.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("tokens=%d err=%v", count, err)
		}
	}
}

func TestNativeAudioScanReusesProbesOfUnchangedTracksDB(t *testing.T) {
	f := nativeAudioLaneFixture(t, "audio")
	scan := func() string {
		t.Helper()
		if err := os.WriteFile(f.notify+".io", nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.scan.ScanStorageFolder(t.Context(), f.folder, storagesource.LocationSelection{Location: f.location}); err != nil {
			t.Fatal(err)
		}
		audit, err := os.ReadFile(f.notify + ".io")
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files WHERE media_folder_id=$1 AND duration>=2 AND missing_since IS NULL`, f.folder.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("publication=%d err=%v", count, err)
		}
		return string(audit)
	}
	if audit := scan(); !strings.Contains(audit, "read track") {
		t.Fatalf("first scan did not probe: %s", audit)
	}
	if audit := scan(); strings.Contains(audit, "read track") || strings.Contains(audit, "stat track") {
		t.Fatalf("unchanged track opened again: %s", audit)
	}
	var kept int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_audio_probes WHERE location_id=$1 AND revision='version:one' AND status='ok'`, f.location.ID).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("kept probes=%d err=%v", kept, err)
	}
	// A new revision is probed again and replaces the kept probe.
	if _, err := f.registry.ReplaceConfiguration(t.Context(), f.installed.Source.Key, f.installed.Source.OwnerID, map[string]map[string]any{"source": {"mode": "audio", "notify": f.notify, "revision": "version:two"}}); err != nil {
		t.Fatal(err)
	}
	if audit := scan(); !strings.Contains(audit, "read track") {
		t.Fatalf("changed revision not probed: %s", audit)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_audio_probes WHERE location_id=$1 AND revision='version:two'`, f.location.ID).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("replaced probes=%d err=%v", kept, err)
	}
}

// audioCatalog drives the audio-catalog fixture through repeated scans.
type audioCatalog struct {
	t *testing.T
	f nativeMovieScanFixture
}

func (c audioCatalog) write(name, value string) {
	c.t.Helper()
	if err := os.WriteFile(c.f.notify+"."+name, []byte(value), 0o600); err != nil {
		c.t.Fatal(err)
	}
}

// scan runs one scan and returns the provider's audit of its I/O.
func (c audioCatalog) scan() string {
	c.t.Helper()
	c.write("io", "")
	if _, err := c.f.scan.ScanStorageFolder(c.t.Context(), c.f.folder, storagesource.LocationSelection{Location: c.f.location}); err != nil {
		c.t.Fatal(err)
	}
	audit, err := os.ReadFile(c.f.notify + ".io")
	if err != nil {
		c.t.Fatal(err)
	}
	return string(audit)
}

// present maps each published track to its revision.
func (c audioCatalog) present() map[string]string {
	c.t.Helper()
	rows, err := c.f.pool.Query(c.t.Context(), `SELECT r.entry_id,r.revision FROM bloem_storage_file_refs r JOIN media_files m ON m.id=r.media_file_id WHERE m.media_folder_id=$1 AND m.missing_since IS NULL`, c.f.folder.ID)
	if err != nil {
		c.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, revision string
		if err := rows.Scan(&id, &revision); err != nil {
			c.t.Fatal(err)
		}
		out[id] = revision
	}
	return out
}

func TestNativeAudioCatalogScansCarryUnchangedBooksDB(t *testing.T) {
	c := audioCatalog{t, nativeAudioLaneFixture(t, "audio-catalog")}
	write, scan, present := c.write, c.scan, c.present
	both := map[string]string{"book-1/track": "v1", "book-2/track": "v1"}
	if audit := scan(); !strings.Contains(audit, "list root since=\n") || !strings.Contains(audit, "list book-1 since=") || !strings.Contains(audit, "list book-2 since=") {
		t.Fatalf("first scan: %s", audit)
	}
	if got := present(); !maps.Equal(got, both) {
		t.Fatalf("first publication: %v", got)
	}
	// Nothing changed: the catalog is listed from its token and no book is listed.
	if audit := scan(); !strings.Contains(audit, "list root since=catalog") || strings.Contains(audit, "list book-") || strings.Contains(audit, "read ") {
		t.Fatalf("unchanged scan: %s", audit)
	}
	if got := present(); !maps.Equal(got, both) {
		t.Fatalf("carried books: %v", got)
	}
	// One book changed: only that book is listed, and its new track is probed.
	write("book-2.rev", "v2")
	write("changed", "book-2")
	if audit := scan(); !strings.Contains(audit, "list book-2 since=") || strings.Contains(audit, "list book-1") || !strings.Contains(audit, "read book-2/track") {
		t.Fatalf("changed scan: %s", audit)
	}
	if got := present(); !maps.Equal(got, map[string]string{"book-1/track": "v1", "book-2/track": "v2"}) {
		t.Fatalf("changed publication: %v", got)
	}
	// One book removed: it goes missing, the other stays without a listing.
	write("changed", "")
	write("gone", "book-1")
	write("book-1.removed", "")
	if audit := scan(); strings.Contains(audit, "list book-") {
		t.Fatalf("removal scan: %s", audit)
	}
	if got := present(); !maps.Equal(got, map[string]string{"book-2/track": "v2"}) {
		t.Fatalf("removal publication: %v", got)
	}
}

func TestNativeAudioCatalogScanRejectsOnlyTheUnreadableBookDB(t *testing.T) {
	c := audioCatalog{t, nativeAudioLaneFixture(t, "audio-catalog")}
	// The provider lists book-1 but no longer has its track's bytes.
	c.write("book-1.missing", "")
	if audit := c.scan(); !strings.Contains(audit, "read book-1/track") {
		t.Fatalf("first scan: %s", audit)
	}
	if got := c.present(); !maps.Equal(got, map[string]string{"book-2/track": "v1"}) {
		t.Fatalf("first publication: %v", got)
	}
	var status string
	if err := c.f.pool.QueryRow(t.Context(), `SELECT status FROM bloem_storage_audio_probes WHERE location_id=$1 AND entry_id='book-1/track'`, c.f.location.ID).Scan(&status); err != nil || status != "unavailable" {
		t.Fatalf("kept status=%q err=%v", status, err)
	}
	// Nothing changed in the catalog, but the unreadable book is listed again;
	// the other is carried. Its bytes are back, so it is published.
	if err := os.Remove(c.f.notify + ".book-1.missing"); err != nil {
		t.Fatal(err)
	}
	if audit := c.scan(); !strings.Contains(audit, "list book-1 since=\n") || strings.Contains(audit, "list book-2") {
		t.Fatalf("retry scan: %s", audit)
	}
	if got := c.present(); !maps.Equal(got, map[string]string{"book-1/track": "v1", "book-2/track": "v1"}) {
		t.Fatalf("retried publication: %v", got)
	}
	// Once read, the book is carried like any other.
	if audit := c.scan(); strings.Contains(audit, "list book-") {
		t.Fatalf("unchanged scan: %s", audit)
	}
}
