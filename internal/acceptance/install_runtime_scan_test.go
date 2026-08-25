//go:build integration

package acceptance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/metadata"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/migrations"
)

const acceptanceAPIKey = "acceptance-api-key"

func TestRetainedProvidersInstallAndMatchDeterministically(t *testing.T) {
	pool := newAcceptanceDatabase(t)
	fixtures := newProviderFixtures(t)
	tmdbBinary := buildProvider(t, "../../../bloem-plugin-tmdb", "github.com/Bloem-Media/bloem-plugin-tmdb/provider.defaultBaseURL", fixtures.server.URL)
	tvdbBinary := buildProvider(t, "../../../bloem-plugin-tvdb", "github.com/Bloem-Media/bloem-plugin-tvdb/provider.defaultBaseURL", fixtures.server.URL)
	static := newStaticCatalog(t, tmdbBinary, tvdbBinary)

	repositories := plugins.NewRepositoryStore(pool)
	repository, err := repositories.Create(context.Background(), plugins.CreateRepositoryInput{URL: static.URL + "/catalog.json", DisplayName: "Acceptance staging"})
	if err != nil {
		t.Fatal(err)
	}
	installations := plugins.NewInstallationStore(pool)
	cipher, err := secret.New([]byte("task-8-disposable-acceptance-secret"))
	if err != nil {
		t.Fatal(err)
	}
	configs := plugins.NewRuntimeConfigStore(pool, cipher)
	catalog := plugins.NewCatalogService(repositories, plugins.CatalogServiceOptions{HTTPClient: static.Client(), CurrentOS: runtime.GOOS, CurrentArch: runtime.GOARCH})
	installRoot := t.TempDir()
	installer := plugins.NewInstaller(installations, plugins.InstallerOptions{BaseDir: installRoot, HTTPClient: static.Client()})
	host := pluginhost.NewHost(pluginhost.Config{Logger: hclog.NewNullLogger()})
	t.Cleanup(func() { _ = host.Shutdown(context.Background()) })
	service := plugins.NewService(repositories, installations, configs, catalog, installer, plugins.NewHostAdapter(host))

	// Tamper rejection occurs through the exact external repository ID and
	// leaves neither a row nor an installed file tree.
	static.tamper.Store(true)
	if _, err := service.InstallCatalog(context.Background(), plugins.InstallCatalogRequest{RepositoryID: repository.ID, PluginID: "silo.tmdb", Version: "acceptance"}); err == nil {
		t.Fatal("tampered staged binary installed")
	}
	assertPluginAbsent(t, pool, "silo.tmdb")
	assertNoInstalledFiles(t, installRoot)
	static.tamper.Store(false)

	tmdb := installAndStart(t, service, configs, installations, repository.ID, "silo.tmdb", "tmdb")
	tvdb := installAndStart(t, service, configs, installations, repository.ID, "silo.tvdb", "tvdb")

	movie, err := tmdb.client.Search(context.Background(), &pluginv1.SearchMetadataRequest{Query: "Clockwork Harbor", ItemType: "movie", Year: 2026, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	assertSingleResult(t, movie, "Clockwork Harbor", "4242")
	// A rescan is stable: same provider identity and no additional result.
	movieAgain, err := tmdb.client.Search(context.Background(), &pluginv1.SearchMetadataRequest{Query: "Clockwork Harbor", ItemType: "movie", Year: 2026, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	assertSingleResult(t, movieAgain, "Clockwork Harbor", "4242")

	series, err := tvdb.client.Search(context.Background(), &pluginv1.SearchMetadataRequest{Query: "North Canal", ItemType: "series", Year: 2025, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	assertSingleResult(t, series, "North Canal", "8080")
	ids, _ := structpb.NewStruct(map[string]any{"tvdb": "8080"})
	seasons, err := tvdb.client.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{SeriesProviderId: "8080", ProviderIds: ids, Language: "eng"})
	if err != nil || len(seasons.GetSeasons()) != 1 || seasons.GetSeasons()[0].GetSeasonNumber() != 1 {
		t.Fatalf("seasons=%v err=%v", seasons, err)
	}
	episodes, err := tvdb.client.GetEpisodes(context.Background(), &pluginv1.GetEpisodesRequest{SeriesProviderId: "8080", SeasonNumber: 1, ProviderIds: ids, Language: "eng"})
	if err != nil || len(episodes.GetEpisodes()) != 1 || episodes.GetEpisodes()[0].GetEpisodeNumber() != 1 || episodes.GetEpisodes()[0].GetTitle() != "The Lock Gate" {
		t.Fatalf("episodes=%v err=%v", episodes, err)
	}

	assertRealScanAndRescan(t, pool, service, tmdb.installationID, tvdb.installationID)
	stableBeforeTimeout := scanSnapshot(t, pool)

	// Timeout leaves the previously observed stable provider identity untouched;
	// restoring the fixture returns the same result and identity.
	fixtures.delay.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err = tmdb.client.Search(ctx, &pluginv1.SearchMetadataRequest{Query: "Clockwork Harbor", ItemType: "movie", Year: 2026, Language: "en"})
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && status.Code(err) != codes.DeadlineExceeded) {
		t.Fatalf("timeout error=%v", err)
	}
	if after := scanSnapshot(t, pool); after != stableBeforeTimeout {
		t.Fatalf("provider timeout changed stored metadata: before=%+v after=%+v", stableBeforeTimeout, after)
	}
	fixtures.delay.Store(false)
	recovered, err := tmdb.client.Search(context.Background(), &pluginv1.SearchMetadataRequest{Query: "Clockwork Harbor", ItemType: "movie", Year: 2026, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	assertSingleResult(t, recovered, "Clockwork Harbor", "4242")
	if fixtures.tmdbSearch.Load() < 3 || fixtures.tvdbSearch.Load() < 1 || fixtures.tvdbEpisodes.Load() < 1 {
		t.Fatalf("fixture counters tmdb=%d tvdb=%d episodes=%d", fixtures.tmdbSearch.Load(), fixtures.tvdbSearch.Load(), fixtures.tvdbEpisodes.Load())
	}
}

func newAcceptanceDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminDSN := os.Getenv("BLOEM_ACCEPTANCE_ADMIN_DATABASE_URL")
	if adminDSN == "" {
		t.Fatal("BLOEM_ACCEPTANCE_ADMIN_DATABASE_URL is required")
	}
	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	maintenance := strings.TrimPrefix(u.Path, "/")
	if maintenance == "" {
		t.Fatal("admin URL must name a maintenance database")
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_acceptance_" + hex.EncodeToString(suffix[:])
	if name == maintenance || !strings.HasPrefix(name, "bloem_acceptance_") {
		t.Fatal("unsafe generated database name")
	}
	admin, err := pgx.Connect(context.Background(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close(context.Background()) })
	if _, err := admin.Exec(context.Background(), `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(ctx, `DROP DATABASE `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
	})
	testURL := *u
	testURL.Path = "/" + name
	pool, err := pgxpool.New(context.Background(), testURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(context.Background(), pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	return pool
}

type providerFixtures struct {
	server                               *httptest.Server
	delay                                atomic.Bool
	tmdbSearch, tvdbSearch, tvdbEpisodes atomic.Int64
}

func newProviderFixtures(t *testing.T) *providerFixtures {
	f := &providerFixtures{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if f.delay.Load() && strings.HasPrefix(r.URL.Path, "/search/movie") {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		switch {
		case r.URL.Path == "/configuration":
			json.NewEncoder(w).Encode(map[string]any{"images": map[string]any{"secure_base_url": f.server.URL + "/images/"}})
		case r.URL.Path == "/search/movie":
			f.tmdbSearch.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 4242, "title": "Clockwork Harbor", "original_title": "Clockwork Harbor", "original_language": "en", "release_date": "2026-04-02", "poster_path": "/clockwork.jpg"}}})
		case r.URL.Path == "/movie/4242":
			json.NewEncoder(w).Encode(map[string]any{
				"id": 4242, "title": "Clockwork Harbor", "original_title": "Clockwork Harbor", "original_language": "en",
				"overview": "An invented acceptance movie.", "release_date": "2026-04-02", "runtime": 97,
				"poster_path": "/clockwork.jpg", "backdrop_path": "/clockwork-wide.jpg",
				"external_ids": map[string]any{"imdb_id": "tt4242000"},
				"images":       map[string]any{"posters": []any{map[string]any{"file_path": "/clockwork.jpg", "iso_639_1": "en", "vote_average": 8.0}}, "backdrops": []any{map[string]any{"file_path": "/clockwork-wide.jpg", "vote_average": 8.0}}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/login":
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"token": "fixture-token"}})
		case r.URL.Path == "/search":
			f.tvdbSearch.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": []any{map[string]any{"id": "8080", "tvdb_id": "8080", "name": "North Canal", "year": "2025", "type": "series", "image_url": "tvdb://series/poster.jpg"}}})
		case r.URL.Path == "/series/8080/extended":
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"id": 8080, "name": "North Canal", "originalLanguage": "eng", "image": "tvdb://series/poster.jpg", "seasons": []any{map[string]any{"id": 9001, "seriesId": 8080, "number": 1, "image": "tvdb://season/one.jpg", "type": map[string]any{"id": 1}}}}})
		case strings.HasPrefix(r.URL.Path, "/series/8080/episodes/"):
			f.tvdbEpisodes.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"series": map[string]any{"id": 8080, "name": "North Canal", "originalLanguage": "eng"}, "episodes": []any{map[string]any{"id": 9101, "seriesId": 8080, "name": "The Lock Gate", "seasonNumber": 1, "number": 1, "runtime": 48, "aired": "2025-01-10", "image": "tvdb://episode/one.jpg"}}}})
		default:
			http.Error(w, "fixture path not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

type providerBuild struct {
	binary   []byte
	manifest *pluginv1.PluginManifest
}

func buildProvider(t *testing.T, relativeDir, variable, baseURL string) providerBuild {
	t.Helper()
	dir, _ := filepath.Abs(relativeDir)
	out := filepath.Join(t.TempDir(), "plugin")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version=acceptance -X "+variable+"="+baseURL, "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", dir, err, output)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	manifestCmd := exec.Command(out, "manifest")
	manifestBytes, err := manifestCmd.Output()
	if err != nil {
		t.Fatalf("read executable manifest %s: %v", dir, err)
	}
	manifest := &pluginv1.PluginManifest{}
	if err := protojson.Unmarshal(manifestBytes, manifest); err != nil {
		t.Fatal(err)
	}
	return providerBuild{binary: data, manifest: manifest}
}

type staticCatalog struct {
	*httptest.Server
	tamper   atomic.Bool
	mu       sync.RWMutex
	binaries map[string]providerBuild
}

func newStaticCatalog(t *testing.T, tmdb, tvdb providerBuild) *staticCatalog {
	s := &staticCatalog{binaries: map[string]providerBuild{"tmdb": tmdb, "tvdb": tvdb}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog.json" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(s.index())
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		s.mu.RLock()
		build, ok := s.binaries[name]
		s.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		body := build.binary
		if s.tamper.Load() && name == "tmdb" {
			body = append(append([]byte(nil), body...), 0)
		}
		w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *staticCatalog) index() plugins.RepositoryIndex {
	packages := []plugins.CatalogPackage{}
	for _, item := range []struct{ id, asset string }{{"silo.tmdb", "tmdb"}, {"silo.tvdb", "tvdb"}} {
		build := s.binaries[item.asset]
		sum := fmt.Sprintf("%x", sha256.Sum256(build.binary))
		packages = append(packages, plugins.CatalogPackage{Manifest: build.manifest, Binaries: map[string]plugins.PlatformBinary{runtime.GOOS + "/" + runtime.GOARCH: {URL: item.asset, Checksum: sum}}})
	}
	return plugins.RepositoryIndex{Plugins: packages}
}

type runningProvider struct {
	client         *pluginhost.MetadataProviderClient
	installationID int
}

func installAndStart(t *testing.T, service *plugins.Service, configs *plugins.RuntimeConfigStore, store *plugins.InstallationStore, repositoryID int, pluginID, capabilityID string) runningProvider {
	t.Helper()
	result, err := service.InstallCatalog(context.Background(), plugins.InstallCatalogRequest{RepositoryID: repositoryID, PluginID: pluginID, Version: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Installation.RepositoryID == nil || *result.Installation.RepositoryID != repositoryID || result.Manifest.GetPluginId() != pluginID {
		t.Fatal("installation identity mismatch")
	}
	archive, err := store.GetArchive(context.Background(), result.Installation.ID)
	if err != nil || archive.Checksum == "" || len(archive.Bytes) == 0 {
		t.Fatalf("archive=%v err=%v", archive, err)
	}
	caps, err := store.ListCapabilities(context.Background(), result.Installation.ID)
	if err != nil || len(caps) == 0 {
		t.Fatalf("capabilities=%v err=%v", caps, err)
	}
	if err := configs.PutGlobalConfig(context.Background(), result.Installation.ID, "api_key", map[string]any{"value": acceptanceAPIKey}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(context.Background(), result.Installation.ID); err != nil {
		t.Fatal(err)
	}
	client, err := service.MetadataProviderClient(context.Background(), result.Installation.ID, capabilityID)
	if err != nil {
		t.Fatal(err)
	}
	return runningProvider{client: client, installationID: result.Installation.ID}
}

func assertRealScanAndRescan(t *testing.T, pool *pgxpool.Pool, service *plugins.Service, tmdbID, tvdbID int) {
	t.Helper()
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("ffprobe is required for the acceptance scan")
	}
	root := t.TempDir()
	movieRoot := filepath.Join(root, "movies")
	seriesRoot := filepath.Join(root, "series")
	moviePath := filepath.Join(movieRoot, "Clockwork Harbor (2026)", "Clockwork Harbor (2026).mp4")
	episodePath := filepath.Join(seriesRoot, "North Canal (2025)", "Season 01", "North Canal - S01E01 - The Lock Gate.mp4")
	copyFixture(t, "../scanner/testdata/test.mp4", moviePath)
	copyFixture(t, "../scanner/testdata/test.mp4", episodePath)

	folders := catalog.NewFolderRepository(pool)
	movieFolder, err := folders.Create(context.Background(), catalog.CreateFolderInput{Paths: []string{movieRoot}, Type: "movies", Name: "Acceptance Movies", MetadataLanguage: "en"})
	if err != nil {
		t.Fatal(err)
	}
	seriesFolder, err := folders.Create(context.Background(), catalog.CreateFolderInput{Paths: []string{seriesRoot}, Type: "series", Name: "Acceptance Series", MetadataLanguage: "en"})
	if err != nil {
		t.Fatal(err)
	}
	chains := metadata.NewChainRepository(pool)
	if err := chains.SetChain(context.Background(), movieFolder.ID, []metadata.ChainEntry{{PluginInstallationID: tmdbID, CapabilityID: "tmdb", ContentLevel: "movie", Priority: 1, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	tvEntries := make([]metadata.ChainEntry, 0, 3)
	for priority, level := range []string{"series", "season", "episode"} {
		tvEntries = append(tvEntries, metadata.ChainEntry{PluginInstallationID: tvdbID, CapabilityID: "tvdb", ContentLevel: level, Priority: priority + 1, Enabled: true})
	}
	if err := chains.SetChain(context.Background(), seriesFolder.ID, tvEntries); err != nil {
		t.Fatal(err)
	}

	files := scanner.NewFileRepository(pool)
	mediaScanner := scanner.NewScanner(files, ffprobe, nil, 1, false, 0)
	skipped := metadata.NewSkippedRootRepository(pool)
	metadataService := metadata.NewMetadataService(
		chains,
		metadata.NewPluginResolverAdapter(service),
		service,
		catalog.NewItemRepository(pool),
		catalog.NewProviderIDRepository(pool),
		catalog.NewEpisodeRepository(pool),
		catalog.NewSeasonRepository(pool),
		catalog.NewLibraryItemRepository(pool),
		folders,
		catalog.NewPersonRepository(pool),
		files,
		skipped,
		metadata.NewStaleMediaIDRepository(pool),
		catalog.NewRootClaimRepository(pool),
	)
	worker := metadata.NewMatchWorker(metadataService, files, 1, 10, 30*time.Second)
	ingest := libraryingest.NewExecutor(mediaScanner, worker, folders, skipped, nil, nil)

	for _, folder := range []*models.MediaFolder{movieFolder, seriesFolder} {
		if _, err := ingest.IngestFolder(context.Background(), folder); err != nil {
			t.Fatalf("first ingest %s: %v", folder.Name, err)
		}
	}
	first := scanSnapshot(t, pool)
	assertScanSnapshot(t, pool, first)
	for _, folder := range []*models.MediaFolder{movieFolder, seriesFolder} {
		if _, err := ingest.IngestFolder(context.Background(), folder); err != nil {
			t.Fatalf("rescan %s: %v", folder.Name, err)
		}
	}
	second := scanSnapshot(t, pool)
	if first != second {
		t.Fatalf("rescan changed stable row counts: first=%+v second=%+v", first, second)
	}
}

func copyFixture(t *testing.T, source, destination string) {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, body, 0644); err != nil {
		t.Fatal(err)
	}
}

type storedScanSnapshot struct {
	files, items, providerIDs, libraryLinks, episodeLinks, seasons, episodes int
	movieState, seriesState, episodeState                                    string
}

func scanSnapshot(t *testing.T, pool *pgxpool.Pool) storedScanSnapshot {
	t.Helper()
	var got storedScanSnapshot
	queries := []struct {
		dest *int
		q    string
	}{
		{&got.files, `SELECT count(*) FROM media_files`},
		{&got.items, `SELECT count(*) FROM media_items WHERE title IN ('Clockwork Harbor','North Canal')`},
		{&got.providerIDs, `SELECT count(*) FROM media_item_provider_ids WHERE (provider='tmdb' AND provider_id='4242') OR (provider='tvdb' AND provider_id='8080')`},
		{&got.libraryLinks, `SELECT count(*) FROM media_item_libraries`},
		{&got.episodeLinks, `SELECT count(*) FROM episode_libraries`},
		{&got.seasons, `SELECT count(*) FROM seasons`},
		{&got.episodes, `SELECT count(*) FROM episodes`},
	}
	for _, query := range queries {
		if err := pool.QueryRow(context.Background(), query.q).Scan(query.dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(context.Background(), `SELECT concat_ws('|', title, year::text, poster_path, backdrop_path, tmdb_id) FROM media_items WHERE title='Clockwork Harbor'`).Scan(&got.movieState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT concat_ws('|', title, year::text, poster_path, tvdb_id) FROM media_items WHERE title='North Canal'`).Scan(&got.seriesState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT concat_ws('|', title, season_number::text, episode_number::text, still_path) FROM episodes WHERE title='The Lock Gate'`).Scan(&got.episodeState); err != nil {
		t.Fatal(err)
	}
	return got
}

func assertScanSnapshot(t *testing.T, pool *pgxpool.Pool, got storedScanSnapshot) {
	t.Helper()
	if got.files != 2 || got.items != 2 || got.providerIDs != 2 || got.libraryLinks != 2 || got.episodeLinks != 1 || got.seasons != 1 || got.episodes != 1 {
		t.Fatalf("unexpected persisted scan snapshot: %+v", got)
	}
	var artworkCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM media_items WHERE (title='Clockwork Harbor' AND poster_path LIKE 'tmdb://%') OR (title='North Canal' AND poster_path LIKE 'tvdb://%')`).Scan(&artworkCount); err != nil || artworkCount != 2 {
		t.Fatalf("provider artwork schemes count=%d err=%v", artworkCount, err)
	}
}

func assertPluginAbsent(t *testing.T, pool *pgxpool.Pool, pluginID string) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM plugin_installations WHERE plugin_id=$1`, pluginID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("tamper left installation count=%d err=%v", count, err)
	}
}

func assertNoInstalledFiles(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("tamper left installed files: %v", entries)
	}
}

func assertSingleResult(t *testing.T, response *pluginv1.SearchMetadataResponse, title, providerID string) {
	t.Helper()
	if len(response.GetResults()) != 1 || response.GetResults()[0].GetTitle() != title || response.GetResults()[0].GetProviderId() != providerID {
		t.Fatalf("results=%v", response.GetResults())
	}
}
