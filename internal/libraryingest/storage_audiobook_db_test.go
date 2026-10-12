//go:build integration

package libraryingest

import (
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
				if err == nil {
					t.Fatal("revision conflict published")
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
