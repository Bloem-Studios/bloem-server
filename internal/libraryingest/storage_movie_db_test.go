//go:build integration

package libraryingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nativeMovieScanFixture struct {
	pool      *pgxpool.Pool
	scan      *StorageScanner
	folder    *models.MediaFolder
	location  storagesource.Location
	registry  *plugins.NativeStorageRegistry
	installed *plugins.NativeStorageSnapshot
	notify    string
}

func nativeMovieScanArtifact(t *testing.T) ([]byte, *publicv1.PluginManifest) {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	binaryPath := filepath.Join(t.TempDir(), "native-consumer")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binaryPath, "./testdata/nativeconsumer")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build native consumer: %v\n%s", err, out)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	manifest := &publicv1.PluginManifest{PluginId: "bloem.consumer.fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: hex.EncodeToString(sum[:]),
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}},
		GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "source", JsonSchema: `{"type":"object","properties":{"mode":{"type":"string"},"notify":{"type":"string"},"revision":{"type":"string"}},"additionalProperties":false}`}}}
	return binary, manifest
}

func newNativeMovieScanFixture(t *testing.T, binary []byte, manifest *publicv1.PluginManifest, ready bool, mode string) nativeMovieScanFixture {
	t.Helper()
	pool := scanAdmissionDatabase(t)
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{
		"fixture": {Manifest: manifest, Checksum: manifest.Checksum, OS: runtime.GOOS, Arch: runtime.GOARCH},
	})
	if err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	notify := filepath.Join(t.TempDir(), "movie-source")
	for _, name := range []string{"test.mp4", "subtitles.mkv"} {
		data, err := os.ReadFile(filepath.Join("..", "scanner", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(notify+filepath.Ext(name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	installed, err := registry.Install(t.Context(), plugins.NativeStorageInstallRequest{ArtifactKey: "fixture", Binary: binary,
		Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: "books", RootEntryID: "root", Enabled: true},
		Config: map[string]map[string]any{"source": {"mode": mode, "notify": notify, "revision": "version:one"}}})
	if err != nil {
		t.Fatal(err)
	}
	locations := nativestorage.NewLibraryLocations(pool)
	locations.SetNativeMovieReady(ready)
	folders := catalog.NewFolderRepository(pool)
	folder, err := folders.Create(t.Context(), catalog.CreateFolderInput{Type: "movies", Name: "Synthetic native movies",
		AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
			return locations.AttachTx(ctx, tx, installed.Source.Key, id)
		}})
	if !ready {
		if !errors.Is(err, nativestorage.ErrLocationSourceUnavailable) {
			t.Fatalf("default attachment hold lost: %v", err)
		}
		// Construct only the independently held scan boundary: no location or
		// catalog row was committed by the rejected attachment.
		return nativeMovieScanFixture{pool: pool, folder: &models.MediaFolder{ID: 1, Type: "movies", Enabled: true}, location: storagesource.Location{FolderID: 1}}
	}
	if err != nil {
		t.Fatal(err)
	}
	sources := storagesource.NewRepository(pool)
	location, ok, err := sources.FolderLocation(t.Context(), folder.ID)
	if err != nil || !ok {
		t.Fatal("movie location missing", err)
	}
	host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{HealthInterval: time.Hour})}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("ffprobe required for native movie ingestion")
	}
	publisher := scanner.NewScanner(scanner.NewFileRepository(pool), ffprobe, nil, 1, true, 0)
	resources := resourcetenancy.NewStore(pool)
	scan, err := NewStorageScanner(host, sources, resources, publisher)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := nativestorage.NewScanReader(pool, host)
	if err != nil {
		t.Fatal(err)
	}
	limits := scanner.NativeProbeConfig{Input: nativestorage.ToolInputConfig{MaxBytes: 8 << 20, ChunkBytes: 4096, MaxConcurrentReads: 2, Timeout: 10 * time.Second}, MaxJSONBytes: 256 << 10, MaxStderrBytes: 16 << 10}
	if err := scan.ConfigureNativeMovies(true, reader, limits); err != nil {
		t.Fatal(err)
	}
	return nativeMovieScanFixture{pool, scan, folder, location, registry, installed, notify}
}

// Removing either readiness guard must admit a movie attachment or start a
// scan before the playback integration is ready.
func TestNativeMovieStorageDefaultHold(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeMovieScanFixture(t, binary, manifest, false, "movies")
	if _, err := (&StorageScanner{}).ScanStorageFolder(t.Context(), f.folder, f.location); err == nil {
		t.Fatal("default movie scan hold lost")
	}
	var locations, files int
	if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM library_storage_locations),(SELECT count(*) FROM media_files)`).Scan(&locations, &files); err != nil {
		t.Fatal(err)
	}
	if locations != 0 || files != 0 {
		t.Fatalf("held movie committed locations=%d files=%d", locations, files)
	}
}

// Replacing pinned bytes with a URL, losing the exact persisted revision or
// treating a replacement as a new catalog file breaks these assertions.
func TestNativeMovieStorageScanPublishesPinnedBytes(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeMovieScanFixture(t, binary, manifest, true, "movies")
	result, err := f.scan.ScanStorageFolder(t.Context(), f.folder, f.location)
	if err != nil {
		t.Fatal(err)
	}
	if result.ScanResult.New != 2 {
		t.Fatalf("new movie files = %d", result.ScanResult.New)
	}
	var fileID int
	var contentID, kind, title, codec, container, revision, logical, probeSource, filePath string
	var duration, width, subtitles int
	if err := f.pool.QueryRow(t.Context(), `SELECT f.id,f.content_id,i.type,i.title,f.codec_video,f.container,f.duration,
		(f.video_tracks->0->>'width')::int,r.revision,r.logical_path,f.probe_source,f.file_path
		FROM media_files f JOIN media_items i ON i.content_id=f.content_id JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
		WHERE f.media_folder_id=$1 AND r.entry_id='01-movie'`, f.folder.ID).Scan(&fileID, &contentID, &kind, &title, &codec, &container, &duration, &width, &revision, &logical, &probeSource, &filePath); err != nil {
		t.Fatal(err)
	}
	if kind != "movie" || title != "Native Movie" || codec != "h264" || container != "mp4" || duration != 1 || width != 320 || revision != "version:one" || logical != "Movies//../Native.Movie.2024.mp4" || probeSource != "storage" {
		t.Fatalf("published movie kind=%s title=%s codec=%s container=%s duration=%d width=%d revision=%s logical=%s probe=%s", kind, title, codec, container, duration, width, revision, logical, probeSource)
	}
	if !strings.HasPrefix(filePath, "bloem-storage:") || strings.Contains(filePath, "127.0.0.1") {
		t.Fatal("movie persisted a playable URL instead of its typed catalog key")
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT jsonb_array_length(f.subtitle_tracks) FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE f.media_folder_id=$1 AND r.entry_id='02-movie'`, f.folder.ID).Scan(&subtitles); err != nil || subtitles == 0 {
		t.Fatal("Matroska tracks missing", err)
	}
	audit, err := os.ReadFile(f.notify + ".io")
	if err != nil || !bytesContainAll(audit, "read 01-movie", "read 02-movie") || strings.Contains(string(audit), "read 03-shortcut") {
		t.Fatal("scan did not probe only pinned movie bytes", err)
	}
	result, err = f.scan.ScanStorageFolder(t.Context(), f.folder, f.location)
	if err != nil || result.ScanResult.Unchanged != 3 {
		t.Fatalf("rescan %+v %v", result, err)
	}
	// A user's match may relink this file to a different catalog identity.
	// Rescanning its bytes must keep that chosen identity and metadata.
	contentID = "native-movie-selected-match"
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO media_items(content_id,type,title,sort_title,year,status)
		VALUES($1,'movie','Chosen Movie','Chosen Movie',1979,'matched')`, contentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE media_files SET content_id=$1 WHERE id=$2`, contentID, fileID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.ReplaceConfiguration(t.Context(), f.installed.Source.Key, f.installed.Source.OwnerID, map[string]map[string]any{"source": {"mode": "movies", "notify": f.notify, "revision": "version:two"}}); err != nil {
		t.Fatal(err)
	}
	result, err = f.scan.ScanStorageFolder(t.Context(), f.folder, f.location)
	if err != nil || result.ScanResult.Updated != 2 {
		t.Fatalf("replacement %+v %v", result, err)
	}
	var newID int
	var newContentID, status string
	var year int
	if err := f.pool.QueryRow(t.Context(), `SELECT f.id,f.content_id,r.revision,i.title,i.year,i.status
		FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id JOIN media_items i ON i.content_id=f.content_id
		WHERE f.media_folder_id=$1 AND r.entry_id='01-movie'`, f.folder.ID).Scan(&newID, &newContentID, &revision, &title, &year, &status); err != nil {
		t.Fatal(err)
	}
	if newID != fileID || newContentID != contentID || revision != "version:two" || title != "Chosen Movie" || year != 1979 || status != "matched" {
		t.Fatal("replacement lost the selected movie identity, metadata, or pinned revision")
	}
}

func bytesContainAll(data []byte, terms ...string) bool {
	for _, term := range terms {
		if !strings.Contains(string(data), term) {
			return false
		}
	}
	return true
}

// A late provider failure after complete payload bytes must prevent page and
// catalog commit, even if ffprobe could have accepted its earlier output.
func TestNativeMovieStorageProbeFailureCommitsNothing(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	for _, mode := range []string{"movies-late", "movies-stat-conflict", "movies-no-pin", "movies-invalid"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeMovieScanFixture(t, binary, manifest, true, mode)
			if _, err := f.scan.ScanStorageFolder(t.Context(), f.folder, f.location); err == nil {
				t.Fatal("failed pinned movie probe accepted")
			}
			var files, memberships, entries int
			if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM media_files WHERE media_folder_id=$1),(SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1),(SELECT count(*) FROM bloem_storage_entries WHERE source_key=$2)`, f.folder.ID, f.installed.Source.Key).Scan(&files, &memberships, &entries); err != nil {
				t.Fatal(err)
			}
			if files != 0 || memberships != 0 || entries != 0 {
				t.Fatalf("failed probe committed files=%d memberships=%d entries=%d", files, memberships, entries)
			}
		})
	}
}

// Empty listings do not prove absence. Only explicit removals make movie
// files missing, and the successful authorized completion sweeps them.
func TestNativeMovieStorageRemovalAndEmptyListing(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	for _, mode := range []string{"movies-empty", "movies-removed"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeMovieScanFixture(t, binary, manifest, true, "movies")
			if _, err := f.scan.ScanStorageFolder(t.Context(), f.folder, f.location); err != nil {
				t.Fatal(err)
			}
			if _, err := f.registry.ReplaceConfiguration(t.Context(), f.installed.Source.Key, f.installed.Source.OwnerID,
				map[string]map[string]any{"source": {"mode": mode, "notify": f.notify, "revision": "version:two"}}); err != nil {
				t.Fatal(err)
			}
			result, err := f.scan.ScanStorageFolder(t.Context(), f.folder, f.location)
			if err != nil {
				t.Fatal(err)
			}
			var files, memberships, items int
			var state string
			if err := f.pool.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM media_files WHERE media_folder_id=$1),
				(SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1),
				(SELECT count(*) FROM media_items WHERE type='movie'),
				(SELECT state FROM bloem_storage_scan_runs WHERE source_key=$2 ORDER BY created_at DESC LIMIT 1)`,
				f.folder.ID, f.installed.Source.Key).Scan(&files, &memberships, &items, &state); err != nil {
				t.Fatal(err)
			}
			wantRemaining, wantDeleted, wantMissing := 2, 0, 0
			if mode == "movies-removed" {
				wantRemaining, wantDeleted, wantMissing = 0, 2, 2
			}
			if state != "complete" || files != wantRemaining || memberships != wantRemaining || items != wantRemaining ||
				result.ScanResult.FilesDeleted != wantDeleted || result.ScanResult.Missing != wantMissing {
				t.Fatalf("%s: state=%s files=%d memberships=%d items=%d deleted=%d missing=%d", mode, state,
					files, memberships, items, result.ScanResult.FilesDeleted, result.ScanResult.Missing)
			}
		})
	}
}

// Revoking authority while a real pinned read is pending must prevent both
// the file and its listing checkpoint from being published.
func TestNativeMovieStorageRevocationDuringRead(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	for _, revoked := range []string{"library", "installation"} {
		t.Run(revoked, func(t *testing.T) {
			f := newNativeMovieScanFixture(t, binary, manifest, true, "movies-read-gate")
			// No periodic renewal can conceal a missing post-read authority check.
			f.scan.leaseTTL = time.Hour
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() { _, err := f.scan.ScanStorageFolder(ctx, f.folder, f.location); finished <- err }()
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
		waiting:
			for {
				select {
				case <-poll.C:
					if marker, err := os.ReadFile(f.notify); err == nil && string(marker) == "read" {
						break waiting
					}
				case err := <-finished:
					t.Fatalf("scan returned before pinned read: %v", err)
				case <-ctx.Done():
					t.Fatal("pinned read did not reach gate")
				}
			}
			query, target := "UPDATE media_folders SET enabled=false WHERE id=$1", f.folder.ID
			if revoked == "installation" {
				query, target = "UPDATE plugin_installations SET enabled=false WHERE id=$1", f.installed.Installation.ID
			}
			if _, err := f.pool.Exec(t.Context(), query, target); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.notify+".release", []byte("release"), 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				if err == nil {
					t.Fatal("revoked pinned movie accepted")
				}
			case <-ctx.Done():
				t.Fatal("revoked movie scan did not finish")
			}
			var files, memberships, entries, cursors int
			if err := f.pool.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM media_files WHERE media_folder_id=$1),
				(SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1),
				(SELECT count(*) FROM bloem_storage_entries WHERE source_key=$2),
				(SELECT count(*) FROM bloem_storage_scan_cursors c JOIN bloem_storage_scan_runs r ON r.id=c.run_id WHERE r.source_key=$2)`,
				f.folder.ID, f.installed.Source.Key).Scan(&files, &memberships, &entries, &cursors); err != nil {
				t.Fatal(err)
			}
			if files != 0 || memberships != 0 || entries != 0 || cursors != 0 {
				t.Fatalf("revoked movie committed files=%d memberships=%d entries=%d cursors=%d", files, memberships, entries, cursors)
			}
		})
	}
}
