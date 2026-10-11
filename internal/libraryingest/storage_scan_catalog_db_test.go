//go:build integration

package libraryingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// catalogSource mirrors the native consumer's "catalog" mode state
// (testdata/nativeconsumer/catalog.go): a source a test rewrites between
// scans without changing the provider's configuration.
type catalogSource struct {
	Directories []catalogDirectory `json:"directories"`
	Token       string             `json:"token,omitempty"`
	Refuse      []string           `json:"refuse,omitempty"`
	Fail        []string           `json:"fail,omitempty"`
}

type catalogDirectory struct {
	ID      string        `json:"id"`
	Files   []catalogBook `json:"files"`
	Changed []string      `json:"changed,omitempty"`
	Removed []string      `json:"removed,omitempty"`
}

type catalogBook struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Revision string `json:"revision"`
}

// catalogScan is an ebook library on the native consumer's catalog source,
// scanned through the real storage scan path and plugin runtime.
type catalogScan struct {
	t        *testing.T
	pool     *pgxpool.Pool
	folder   *models.MediaFolder
	location storagesource.Location
	scan     *StorageScanner
	catalog  string
}

func newCatalogScan(t *testing.T) *catalogScan {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	binaryPath := filepath.Join(t.TempDir(), "native-consumer")
	build := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, "./testdata/nativeconsumer")
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
	if err := pool.QueryRow(ctx, "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	notify := filepath.Join(t.TempDir(), "catalog")
	installed, err := registry.Install(ctx, plugins.NativeStorageInstallRequest{ArtifactKey: "fixture", Binary: binary,
		Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: "books", RootEntryID: "root", Enabled: true},
		Config: map[string]map[string]any{"source": {"mode": "catalog", "notify": notify}}})
	if err != nil {
		t.Fatal(err)
	}
	locations := nativestorage.NewLibraryLocations(pool)
	folder, err := catalog.NewFolderRepository(pool).Create(ctx, catalog.CreateFolderInput{Type: "ebooks", Name: "Catalog source",
		AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
			return locations.AttachTx(ctx, tx, installed.Source.Key, id)
		}})
	if err != nil {
		t.Fatal(err)
	}
	sources := storagesource.NewRepository(pool)
	location, ok, err := sources.FolderLocation(ctx, folder.ID)
	if err != nil || !ok {
		t.Fatal("storage location missing", err)
	}
	host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{HealthInterval: time.Hour})}
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := host.Shutdown(shutdown); err != nil {
			t.Error(err)
		}
	})
	// Production's removal grace keeps files marked missing observable.
	publisher := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, true, 24*time.Hour)
	scan, err := NewStorageScanner(host, sources, resourcetenancy.NewStore(pool), publisher)
	if err != nil {
		t.Fatal(err)
	}
	scan.leaseTTL = time.Hour
	return &catalogScan{t: t, pool: pool, folder: folder, location: location, scan: scan, catalog: notify + ".catalog"}
}

func (c *catalogScan) write(source catalogSource) {
	c.t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(c.catalog, raw, 0600); err != nil {
		c.t.Fatal(err)
	}
}

func (c *catalogScan) run() (*scanner.ScanResult, error) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(c.t.Context(), time.Minute)
	defer cancel()
	result, err := c.scan.ScanStorageFolder(ctx, c.folder, c.location)
	if result == nil || result.ScanResult == nil {
		c.t.Fatalf("scan returned no result: %v", err)
	}
	return result.ScanResult, err
}

func (c *catalogScan) mustRun() *scanner.ScanResult {
	c.t.Helper()
	result, err := c.run()
	if err != nil {
		c.t.Fatal(err)
	}
	return result
}

// lastRun is the newest discovery run of the library's source.
func (c *catalogScan) lastRun() (id uuid.UUID, state string) {
	c.t.Helper()
	if err := c.pool.QueryRow(c.t.Context(), `SELECT id,state FROM bloem_storage_scan_runs WHERE source_key=$1 ORDER BY created_at DESC,id LIMIT 1`,
		c.location.SourceKey).Scan(&id, &state); err != nil {
		c.t.Fatal(err)
	}
	return id, state
}

// catalogFile is one published book's catalog rows, with the transaction IDs
// that last wrote them.
type catalogFile struct {
	Title                                   string
	Missing, Member                         bool
	FileXmin, ItemXmin, RefXmin, MemberXmin string
	CreditXmins                             string
	FileUpdated, ItemUpdated                time.Time
}

func (c *catalogScan) files() map[string]catalogFile {
	c.t.Helper()
	rows, err := c.pool.Query(c.t.Context(), `SELECT r.entry_id,i.title,f.missing_since IS NOT NULL,m.content_id IS NOT NULL,
		f.xmin::text,i.xmin::text,r.xmin::text,COALESCE(m.xmin::text,''),
		COALESCE((SELECT string_agg(p.xmin::text,',' ORDER BY p.id) FROM item_people p WHERE p.content_id=f.content_id),''),
		f.updated_at,i.updated_at
		FROM bloem_storage_file_refs r JOIN media_files f ON f.id=r.media_file_id JOIN media_items i ON i.content_id=f.content_id
		LEFT JOIN media_item_libraries m ON m.content_id=f.content_id AND m.media_folder_id=f.media_folder_id
		WHERE r.location_id=$1`, c.location.ID)
	if err != nil {
		c.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]catalogFile{}
	for rows.Next() {
		var id string
		var f catalogFile
		if err := rows.Scan(&id, &f.Title, &f.Missing, &f.Member, &f.FileXmin, &f.ItemXmin, &f.RefXmin, &f.MemberXmin, &f.CreditXmins, &f.FileUpdated, &f.ItemUpdated); err != nil {
			c.t.Fatal(err)
		}
		out[id] = f
	}
	if err := rows.Err(); err != nil {
		c.t.Fatal(err)
	}
	return out
}

// A forced full re-listing of unchanged books must not rewrite their catalog
// rows, while the journal still records that this listing saw them.
func TestStorageScanSkipsUnchangedRepublication(t *testing.T) {
	c := newCatalogScan(t)
	books := []catalogBook{{ID: "a", Title: "Alpha", Revision: "r1"}, {ID: "b", Title: "Beta", Revision: "r1"}, {ID: "c", Title: "Gamma", Revision: "r1"}}
	c.write(catalogSource{Directories: []catalogDirectory{{ID: "epub", Files: books}}})
	if got := c.mustRun(); got.New != 3 || got.Updated != 0 || got.Unchanged != 0 {
		t.Fatalf("first scan = %+v", got)
	}
	firstRun, _ := c.lastRun()
	before := c.files()
	if len(before) != 3 {
		t.Fatalf("published files = %d", len(before))
	}

	// The source issues no change token, so this is a complete full listing.
	got := c.mustRun()
	secondRun, state := c.lastRun()
	if got.New != 0 || got.Updated != 0 || got.Unchanged != 3 || state != "complete" || secondRun == firstRun {
		t.Fatalf("unchanged re-list = %+v run=%s state=%s", got, secondRun, state)
	}
	after := c.files()
	for id, want := range before {
		if after[id] != want {
			t.Errorf("unchanged re-list rewrote %s:\n before %+v\n after  %+v", id, want, after[id])
		}
	}
	var seen int
	if err := c.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_entries WHERE source_key=$1 AND kind=1 AND last_seen_run=$2`,
		c.location.SourceKey, secondRun).Scan(&seen); err != nil || seen != 3 {
		t.Fatalf("journal last_seen_run not advanced: %d %v", seen, err)
	}

	// A metadata-only change keeps its revision and still republishes that book.
	books[1].Title = "Beta Revised"
	c.write(catalogSource{Directories: []catalogDirectory{{ID: "epub", Files: books}}})
	if got := c.mustRun(); got.New != 0 || got.Updated != 1 || got.Unchanged != 2 {
		t.Fatalf("metadata change = %+v", got)
	}
	changed := c.files()
	if changed["epub/b"].Title != "Beta Revised" || changed["epub/b"].ItemXmin == after["epub/b"].ItemXmin {
		t.Fatalf("metadata change not republished: %+v", changed["epub/b"])
	}
	for _, id := range []string{"epub/a", "epub/c"} {
		if changed[id] != after[id] {
			t.Errorf("metadata change of another book rewrote %s", id)
		}
	}

	// A file marked missing is republished when listed again, even unchanged.
	if _, err := c.pool.Exec(t.Context(), `UPDATE media_files f SET missing_since=now() FROM bloem_storage_file_refs r
		WHERE r.media_file_id=f.id AND r.location_id=$1 AND r.entry_id='epub/a'`, c.location.ID); err != nil {
		t.Fatal(err)
	}
	c.mustRun()
	if restored := c.files()["epub/a"]; restored.Missing || !restored.Member {
		t.Fatalf("listed file still missing: %+v", restored)
	}
}
