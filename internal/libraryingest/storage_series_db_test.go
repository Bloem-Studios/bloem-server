//go:build integration

package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/metadata"
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

// The fake provider's verified ClearStream layout (testdata/nativeconsumer/series.go).
const (
	seriesA01 = "television/00s/Show A (2005) {tvdb-111}/Season 01/Show A (2005) - S01E01 [WEBDL-1080p]-Grp"
	seriesA02 = "television/00s/Show A (2005) {tvdb-111}/Season 01/Show A (2005) - S01E02 [WEBDL-1080p]-Grp"
	seriesA00 = "television/00s/Show A (2005) {tvdb-111}/Season 00/Show A (2005) - S00E01 [WEBDL-1080p]-Grp"
	seriesB01 = "television/10s/Show B (2012) {tvdb-222}/Season 01/Show B (2012) - S01E01-E02 [HDTV-720p]-Grp"
)

type nativeSeriesScanFixture struct {
	pool      *pgxpool.Pool
	scan      *StorageScanner
	folder    *models.MediaFolder
	location  storagesource.Location
	installed *plugins.NativeStorageSnapshot
	notify    string
}

func newNativeSeriesScanFixture(t *testing.T, binary []byte, manifest *publicv1.PluginManifest, seriesReady bool) nativeSeriesScanFixture {
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
	notify := filepath.Join(t.TempDir(), "series-source")
	data, err := os.ReadFile(filepath.Join("..", "scanner", "testdata", "subtitles.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notify+".mkv", data, 0o600); err != nil {
		t.Fatal(err)
	}
	installed, err := registry.Install(t.Context(), plugins.NativeStorageInstallRequest{ArtifactKey: "fixture", Binary: binary,
		Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: "books", RootEntryID: "root", Enabled: true},
		Config: map[string]map[string]any{"source": {"mode": "series", "notify": notify, "revision": "version:one"}}})
	if err != nil {
		t.Fatal(err)
	}
	locations := nativestorage.NewLibraryLocations(pool)
	locations.SetNativeMovieReady(true)
	locations.SetNativeSeriesReady(seriesReady)
	folder, err := catalog.NewFolderRepository(pool).Create(t.Context(), catalog.CreateFolderInput{Type: "series", Name: "Synthetic native television",
		AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
			return locations.AttachTx(ctx, tx, installed.Source.Key, id)
		}})
	if !seriesReady {
		if !errors.Is(err, nativestorage.ErrLocationSourceUnavailable) {
			t.Fatalf("default series attachment hold lost: %v", err)
		}
		return nativeSeriesScanFixture{pool: pool, folder: &models.MediaFolder{ID: 1, Type: "series", Enabled: true}, location: storagesource.Location{FolderID: 1}}
	}
	if err != nil {
		t.Fatal(err)
	}
	sources := storagesource.NewRepository(pool)
	location, ok, err := sources.FolderLocation(t.Context(), folder.ID)
	if err != nil || !ok {
		t.Fatal("series location missing", err)
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
		t.Fatal("ffprobe required for native series ingestion")
	}
	publisher := scanner.NewScanner(scanner.NewFileRepository(pool), ffprobe, nil, 1, true, 0)
	scan, err := NewStorageScanner(host, sources, resourcetenancy.NewStore(pool), publisher)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := nativestorage.NewScanReader(pool, host)
	if err != nil {
		t.Fatal(err)
	}
	limits := scanner.NativeProbeConfig{Input: nativestorage.ToolInputConfig{MaxBytes: 8 << 20, ChunkBytes: 4096, MaxConcurrentReads: 2, Timeout: 10 * time.Second}, MaxJSONBytes: 256 << 10, MaxStderrBytes: 16 << 10}
	if err := scan.ConfigureNativeSeries(true); err == nil {
		t.Fatal("series lane composed without the movie reader")
	}
	if err := scan.ConfigureNativeMovies(true, reader, limits); err != nil {
		t.Fatal(err)
	}
	if err := scan.ConfigureNativeSeries(true); err != nil {
		t.Fatal(err)
	}
	return nativeSeriesScanFixture{pool, scan, folder, location, installed, notify}
}

func (f nativeSeriesScanFixture) scanOnce(t *testing.T) *Result {
	t.Helper()
	selections, err := f.scan.LocationSelections(t.Context(), f.folder.ID)
	if err != nil || len(selections) != 1 {
		t.Fatalf("selections %+v %v", selections, err)
	}
	result, err := f.scan.ScanStorageFolder(t.Context(), f.folder, selections[0])
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// fakeSeriesMetadata stands in for Silo's metadata service: an Identify with a
// known TVDB ID creates the provider's episodes and links the files with
// Silo's own bulk linker, as Process does after a series match.
type fakeSeriesMetadata struct {
	files       *scanner.FileRepository
	pool        *pgxpool.Pool
	mu          sync.Mutex
	requests    []metadata.ProcessRequest
	synthesized []string
}

var fakeSeriesEpisodes = map[string][][2]int{"111": {{0, 1}, {1, 1}, {1, 2}}, "222": {{1, 1}, {1, 2}}}

func (m *fakeSeriesMetadata) Process(ctx context.Context, req metadata.ProcessRequest) (*metadata.ProcessResult, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	episodes, known := fakeSeriesEpisodes[req.ProviderIDs["tvdb"]]
	if req.Mode != metadata.ModeIdentify || !known {
		return &metadata.ProcessResult{ContentID: req.ContentID}, nil
	}
	for _, e := range episodes {
		// Silo's episode writer stores an absent still as '', never NULL.
		if _, err := m.pool.Exec(ctx, `INSERT INTO episodes(content_id,series_id,season_number,episode_number,title,still_path) VALUES($1,$2,$3,$4,$1,'')`,
			fmt.Sprintf("%s-s%02de%02d", req.ContentID, e[0], e[1]), req.ContentID, e[0], e[1]); err != nil {
			return nil, err
		}
	}
	if _, err := m.pool.Exec(ctx, `UPDATE media_items SET status='matched' WHERE content_id=$1`, req.ContentID); err != nil {
		return nil, err
	}
	if _, err := m.files.BulkLinkEpisodesBySeries(ctx, req.ContentID); err != nil {
		return nil, err
	}
	return &metadata.ProcessResult{ContentID: req.ContentID, Updated: true}, nil
}

func (m *fakeSeriesMetadata) SynthesizeFallbackEpisodes(_ context.Context, seriesID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.synthesized = append(m.synthesized, seriesID)
	return nil
}

type seriesFileRow struct {
	contentID, kind, title, status, observedRoot string
	season                                       *int
	episode, multiStart, multiEnd                int
	linked                                       bool
	sidecars                                     []string
}

func seriesFiles(t *testing.T, f nativeSeriesScanFixture) map[string]seriesFileRow {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), `SELECT r.entry_id,f.content_id,i.type,i.title,i.status,f.observed_root_path,f.season_number,COALESCE(f.episode_number,0),
		COALESCE(f.multi_episode_start,0),COALESCE(f.multi_episode_end,0),f.episode_id IS NOT NULL,
		COALESCE((SELECT array_agg(c.language||CASE WHEN c.hearing_impaired THEN '.sdh' ELSE '' END ORDER BY c.language,c.hearing_impaired) FROM bloem_storage_episode_sidecars c WHERE c.media_file_id=f.id),'{}')
		FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id JOIN media_items i ON i.content_id=f.content_id
		WHERE f.media_folder_id=$1 AND f.missing_since IS NULL`, f.folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]seriesFileRow{}
	for rows.Next() {
		var id string
		var row seriesFileRow
		if err := rows.Scan(&id, &row.contentID, &row.kind, &row.title, &row.status, &row.observedRoot, &row.season, &row.episode, &row.multiStart, &row.multiEnd, &row.linked, &row.sidecars); err != nil {
			t.Fatal(err)
		}
		out[id] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func seriesAudit(t *testing.T, f nativeSeriesScanFixture) string {
	t.Helper()
	audit, err := os.ReadFile(f.notify + ".io")
	if err != nil {
		t.Fatal(err)
	}
	return string(audit)
}

// Removing either readiness guard must admit a TV attachment or start a scan
// before the series lane is composed.
func TestNativeSeriesStorageDefaultHoldDB(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeSeriesScanFixture(t, binary, manifest, false)
	if _, err := (&StorageScanner{nativeMovieReady: true}).ScanStorageFolder(t.Context(), f.folder, storagesource.LocationSelection{Location: f.location}); err == nil {
		t.Fatal("default series scan hold lost")
	}
	var locations, files int
	if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM library_storage_locations),(SELECT count(*) FROM media_files)`).Scan(&locations, &files); err != nil {
		t.Fatal(err)
	}
	if locations != 0 || files != 0 {
		t.Fatalf("held series committed locations=%d files=%d", locations, files)
	}
}

// A library of two shows in the verified layout publishes one series per show
// across pages and seasons, keeps specials in season 0, attaches subtitle
// sidecars across page boundaries, never lists .downloads/ or extras, matches
// by the folder's TVDB tag and links every episode with Silo's linker. A
// rescan of unchanged files opens nothing.
func TestNativeSeriesStorageScanPublishesShowsDB(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeSeriesScanFixture(t, binary, manifest, true)

	// First without matching: the publication alone.
	result := f.scanOnce(t)
	if result.ScanResult.New != 4 {
		t.Fatalf("new episode files = %d", result.ScanResult.New)
	}
	files := seriesFiles(t, f)
	if len(files) != 4 {
		t.Fatalf("published %d files: %+v", len(files), files)
	}
	want := map[string]struct {
		title               string
		season, episode     int
		multiStart, multiTo int
		sidecars            []string
	}{
		seriesA01 + ".mkv": {"Show A", 1, 1, 0, 0, []string{"en", "en.sdh", "nl"}},
		seriesA02 + ".mkv": {"Show A", 1, 2, 0, 0, []string{"en"}},
		seriesA00 + ".mkv": {"Show A", 0, 1, 0, 0, []string{"en", "nl"}},
		seriesB01 + ".mkv": {"Show B", 1, 1, 1, 2, []string{"zh"}},
	}
	for id, w := range want {
		got, ok := files[id]
		if !ok || got.kind != "series" || got.title != w.title || got.status != "pending" || got.observedRoot != "" || got.season == nil || *got.season != w.season ||
			got.episode != w.episode || got.multiStart != w.multiStart || got.multiEnd != w.multiTo || !slices.Equal(got.sidecars, w.sidecars) {
			t.Errorf("%s: %+v (season %v)", id, got, got.season)
		}
	}
	if files[seriesA01+".mkv"].contentID != files[seriesA00+".mkv"].contentID || files[seriesA01+".mkv"].contentID != files[seriesA02+".mkv"].contentID ||
		files[seriesA01+".mkv"].contentID == files[seriesB01+".mkv"].contentID {
		t.Fatal("episodes did not group into one series per show")
	}
	var items, queued, memberships int
	if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM media_items WHERE type='series'),
		(SELECT count(*) FROM series_root_match_queue WHERE media_folder_id=$1),
		(SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1)`, f.folder.ID).Scan(&items, &queued, &memberships); err != nil {
		t.Fatal(err)
	}
	if items != 2 || queued != 0 || memberships != 2 {
		t.Fatalf("series items=%d queued=%d memberships=%d", items, queued, memberships)
	}
	audit := seriesAudit(t, f)
	for _, id := range []string{seriesA01, seriesA02, seriesA00, seriesB01} {
		if !strings.Contains(audit, "read "+id+".mkv") {
			t.Errorf("episode %s was not probed", id)
		}
	}
	for _, never := range []string{".downloads", "Featurettes", "sample.mkv", "S01E03", ".srt", "poster.jpg", "tvshow.nfo"} {
		for _, line := range strings.Split(audit, "\n") {
			if strings.Contains(line, never) && (strings.HasPrefix(line, "read ") || strings.HasPrefix(line, "stat ") || strings.HasPrefix(line, "list ")) {
				t.Errorf("scan touched %s: %s", never, line)
			}
		}
	}

	// Matching: the folder's TVDB tag identifies each show, and Silo's bulk
	// linker links every file, specials included, from the persisted numbers.
	meta := &fakeSeriesMetadata{files: scanner.NewFileRepository(f.pool), pool: f.pool}
	f.scan.ConfigureNativeSeriesMatching(meta)
	before := len(seriesAudit(t, f))
	result = f.scanOnce(t)
	if result.ScanResult.New != 0 || result.ScanResult.Updated != 0 {
		t.Fatalf("unchanged rescan new=%d updated=%d", result.ScanResult.New, result.ScanResult.Updated)
	}
	for _, line := range strings.Split(seriesAudit(t, f)[before:], "\n") {
		if strings.HasPrefix(line, "read ") || strings.HasPrefix(line, "stat ") {
			t.Fatalf("unchanged rescan opened a file: %s", line)
		}
	}
	if len(meta.requests) != 2 {
		t.Fatalf("match requests %+v", meta.requests)
	}
	for _, req := range meta.requests {
		if req.Mode != metadata.ModeIdentify || (req.ProviderIDs["tvdb"] != "111" && req.ProviderIDs["tvdb"] != "222") {
			t.Fatalf("series matched without its folder tag: %+v", req)
		}
	}
	files = seriesFiles(t, f)
	for id, row := range files {
		if !row.linked || row.status != "matched" {
			t.Errorf("%s not linked after match: %+v", id, row)
		}
	}
	var linkedEpisodes int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM episode_libraries WHERE media_folder_id=$1`, f.folder.ID).Scan(&linkedEpisodes); err != nil {
		t.Fatal(err)
	}
	// Show A: S00E01, S01E01, S01E02; Show B: S01E01-E02 spans two episodes.
	if linkedEpisodes != 5 {
		t.Fatalf("episode memberships = %d", linkedEpisodes)
	}
	var catalogEntries int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM episode_catalog_entries WHERE media_folder_id=$1`, f.folder.ID).Scan(&catalogEntries); err != nil {
		t.Fatal(err)
	}
	if catalogEntries != 5 {
		t.Fatalf("episode catalog entries = %d", catalogEntries)
	}
}

// The completion sweep removes the episode memberships of deleted files, the
// sidecars of deleted subtitles, and a show whose last file is gone.
func TestNativeSeriesStorageCompletionSweepDB(t *testing.T) {
	binary, manifest := nativeMovieScanArtifact(t)
	f := newNativeSeriesScanFixture(t, binary, manifest, true)
	meta := &fakeSeriesMetadata{files: scanner.NewFileRepository(f.pool), pool: f.pool}
	f.scan.ConfigureNativeSeriesMatching(meta)
	f.scanOnce(t)
	files := seriesFiles(t, f)
	showA, showB := files[seriesA01+".mkv"].contentID, files[seriesB01+".mkv"].contentID
	// One episode and one sidecar of Show A go, and Show B's whole group folder.
	removed := strings.Join([]string{seriesA02 + ".mkv", seriesA02 + ".en.srt", seriesA01 + ".nl.srt", "television/10s"}, "\n")
	if err := os.WriteFile(f.notify+".series-removed", []byte(removed), 0o600); err != nil {
		t.Fatal(err)
	}
	// A full listing that omits an entry only suspects its absence; the next
	// complete listing that still omits it confirms it.
	suspected := f.scanOnce(t)
	result := f.scanOnce(t)
	if suspected.ScanResult.Missing != 0 || result.ScanResult.Missing != 2 || result.ScanResult.ItemsDeleted != 1 || result.ScanResult.MembershipsRemoved < 2 {
		t.Fatalf("sweep suspected=%d missing=%d items deleted=%d memberships removed=%d", suspected.ScanResult.Missing, result.ScanResult.Missing, result.ScanResult.ItemsDeleted, result.ScanResult.MembershipsRemoved)
	}
	files = seriesFiles(t, f)
	if len(files) != 2 || !slices.Equal(files[seriesA01+".mkv"].sidecars, []string{"en", "en.sdh"}) {
		t.Fatalf("after removal %+v", files)
	}
	var e02, a01, showBItems, showBMemberships, sidecars int
	if err := f.pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM episode_libraries WHERE media_folder_id=$1 AND episode_id=$2||'-s01e02'),
		(SELECT count(*) FROM episode_libraries WHERE media_folder_id=$1 AND episode_id=$2||'-s01e01'),
		(SELECT count(*) FROM media_items WHERE content_id=$3),
		(SELECT count(*) FROM media_item_libraries WHERE content_id=$3),
		(SELECT count(*) FROM bloem_storage_episode_sidecars WHERE location_id=$4)`, f.folder.ID, showA, showB, f.location.ID).Scan(&e02, &a01, &showBItems, &showBMemberships, &sidecars); err != nil {
		t.Fatal(err)
	}
	if e02 != 0 || a01 != 1 || showBItems != 0 || showBMemberships != 0 || sidecars != 4 {
		t.Fatalf("e02=%d a01=%d showB items=%d memberships=%d sidecars=%d", e02, a01, showBItems, showBMemberships, sidecars)
	}
}
