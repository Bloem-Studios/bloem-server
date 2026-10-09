package nativestorage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// storageSourceFixture installs a platform-owned storage source the way the
// registry does: an enabled plugin installation, its storage marker and source.
func storageSourceFixture(t *testing.T) (*pgxpool.Pool, storagesource.SourceConfig) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	plugin := fmt.Sprintf("storage.fixture.%d", time.Now().UnixNano())
	var installation int64
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id) VALUES($1,'1','/synthetic',$2) RETURNING id`, plugin, owner).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, installation, owner); err != nil {
		t.Fatal(err)
	}
	source, err := storagesource.NewRepository(pool).CreateSource(ctx, storagesource.SourceConfig{OwnerID: owner, InstallationID: &installation, PluginID: plugin, ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM organization_entitlements WHERE media_folder_id IN (SELECT folder_id FROM library_storage_locations WHERE source_key=$1)`, source.Key)
		_, _ = pool.Exec(ctx, `DELETE FROM media_folders WHERE id IN (SELECT folder_id FROM library_storage_locations WHERE source_key=$1)`, source.Key)
		_, _ = pool.Exec(ctx, `DELETE FROM bloem_storage_sources WHERE key=$1`, source.Key)
		_, _ = pool.Exec(ctx, `DELETE FROM organization_entitlements WHERE plugin_installation_id=$1`, installation)
		_, _ = pool.Exec(ctx, `DELETE FROM plugin_installations WHERE id=$1`, installation)
	})
	return pool, source
}

func createStorageLibrary(t *testing.T, pool *pgxpool.Pool, kind string, source uuid.UUID) (int, error) {
	t.Helper()
	locations := NewLibraryLocations(pool)
	folder, err := catalog.NewFolderRepository(pool).Create(t.Context(), catalog.CreateFolderInput{
		Type: kind, Name: "Storage location test",
		AttachStorage: func(ctx context.Context, tx pgx.Tx, folderID int) error {
			return locations.AttachTx(ctx, tx, source, folderID)
		},
	})
	if err != nil {
		return 0, err
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organization_entitlements WHERE media_folder_id=$1`, folder.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_folders WHERE id=$1`, folder.ID)
	})
	if folder.StorageSourceKey == nil || *folder.StorageSourceKey != source || len(folder.Paths) != 0 {
		t.Fatalf("created library location = %v paths=%v", folder.StorageSourceKey, folder.Paths)
	}
	return folder.ID, nil
}

func TestCreateLibraryWithStorageSource(t *testing.T) {
	pool, source := storageSourceFixture(t)
	id, err := createStorageLibrary(t, pool, "ebooks", source.Key)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := catalog.NewFolderRepository(pool).GetByID(t.Context(), id)
	if err != nil || folder.StorageSourceKey == nil || *folder.StorageSourceKey != source.Key {
		t.Fatalf("stored library location = %+v %v", folder, err)
	}
	if _, err := createStorageLibrary(t, pool, "ebooks", source.Key); !errors.Is(err, ErrLocationSourceInUse) {
		t.Fatalf("second library on one source: %v", err)
	}
}

func TestCreateLibraryRefusesUnusableStorageSource(t *testing.T) {
	pool, source := storageSourceFixture(t)
	for name, tc := range map[string]struct {
		kind   string
		source uuid.UUID
		setup  string
	}{
		"not an ebook library": {kind: "movies", source: source.Key},
		"unknown source":       {kind: "ebooks", source: uuid.New()},
		"disabled source":      {kind: "ebooks", source: source.Key, setup: `UPDATE bloem_storage_sources SET enabled=false WHERE key=$1`},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.setup != "" {
				if _, err := pool.Exec(t.Context(), tc.setup, source.Key); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_, _ = pool.Exec(context.Background(), `UPDATE bloem_storage_sources SET enabled=true WHERE key=$1`, source.Key)
				})
			}
			if _, err := createStorageLibrary(t, pool, tc.kind, tc.source); !errors.Is(err, ErrLocationSourceUnavailable) {
				t.Fatalf("unusable source admitted: %v", err)
			}
			var stray int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM library_storage_locations WHERE source_key=$1`, source.Key).Scan(&stray); err != nil || stray != 0 {
				t.Fatalf("refused create left %d locations: %v", stray, err)
			}
		})
	}
}

// The source list query must parse and return a platform source to a platform
// actor; a broken visibility predicate made every listing fail as unavailable.
func TestListSourcesVisibilityQuery(t *testing.T) {
	pool, source := storageSourceFixture(t)
	rows, err := pool.Query(context.Background(), `SELECT s.key FROM bloem_storage_sources s JOIN resource_owners o ON o.id=s.owner_id
 WHERE `+nativeVisibleSourceSQL+` AND ($3::uuid IS NULL OR s.key>$3) ORDER BY s.key LIMIT $4`, true, nil, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if key == source.Key {
			return
		}
	}
	t.Fatalf("platform listing omitted source %s", source.Key)
}
