//go:build integration

package scanner

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PRE-MODE baseline only: this exercises the existing trusted native ingest
// fixture, not the future marked-library/authorized onboarding lifecycle.
// Removing the future retained-file guard would allow the native content link
// to be cleared; this assertion intentionally exposes that missing guard now.
func TestNativeOnboardingRetainedFileGuardDB(t *testing.T) {
	x := nativeOnboardingRetentionPreModeSetup(t)
	id := x.publish(t) // Actual PublishNativeEbook -> transactional publication.
	if id == "" {
		t.Fatal("fixture did not return an actual published content ID")
	}
	var fileID int
	if err := x.pool.QueryRow(t.Context(), `
SELECT f.id FROM media_files f
JOIN media_items i ON i.content_id=f.content_id
JOIN media_item_libraries l ON l.content_id=i.content_id AND l.media_folder_id=f.media_folder_id
JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
WHERE f.content_id=$1 AND f.media_folder_id=$2 AND f.probe_source='native'
  AND r.binding_id=$3 AND r.entry_id='book' AND r.revision='v1'`,
		id, x.folder.ID, x.binding.ID).Scan(&fileID); err != nil {
		t.Fatalf("fixture did not reach actual retained native publication: %v", err)
	}
	if _, ok, err := x.r.NextIngestion(t.Context(), x.claim.Lease); err != nil || ok {
		t.Fatalf("actual publication did not advance the ingestion checkpoint: pending=%v error=%v", ok, err)
	}
	t.Log("PRE-MODE actual native publication committed: item, file, membership, retained ref and checkpoint verified")

	tx, err := x.pool.Begin(t.Context())
	if err != nil {
		t.Fatal("begin retained-file mutation")
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil {
			t.Error("rollback retained-file mutation")
		}
	}()
	tag, mutationErr := tx.Exec(t.Context(),
		"UPDATE media_files SET content_id=NULL WHERE id=$1", fileID)
	if mutationErr == nil {
		var contentLinkCleared bool
		if err := tx.QueryRow(t.Context(), "SELECT content_id IS NULL FROM media_files WHERE id=$1", fileID).Scan(&contentLinkCleared); err != nil {
			t.Fatalf("inspect successful native link mutation: %v", err)
		}
		if tag.RowsAffected() != 1 || !contentLinkCleared {
			t.Fatal("mutation did not change the published native file")
		}
		t.Log("PRE-MODE mutation succeeded: rows=1, published media_files.content_id is NULL inside transaction")
	}
	var pgerr *pgconn.PgError
	if !errors.As(mutationErr, &pgerr) || pgerr.Code != "BN001" {
		t.Fatalf("native link mutation must refuse BN001; actual error class=%T; rows=%d", mutationErr, tag.RowsAffected())
	}
}

func nativeOnboardingRetentionPreModeSetup(t *testing.T) *nativeIngestFixture {
	t.Helper()
	const privateConfig = "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	info, err := os.Stat(privateConfig)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("task-owned mode-0600 database configuration required")
	}
	data, err := os.ReadFile(privateConfig)
	if err != nil {
		t.Fatal("read private database configuration")
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid private database configuration")
	}
	template := cfg.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		t.Fatal("refusing non-owned template")
	}
	adminConfig := cfg.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatal("connect private clone admin")
	}
	clone := "bloem_storage_test_red_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{clone}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal("create private UUID clone")
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		defer admin.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{clone}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("drop private UUID clone")
			return
		}
		var exists bool
		if err := admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", clone).Scan(&exists); err != nil || exists {
			t.Error("private UUID clone cleanup not verified")
			return
		}
		t.Logf("PRE-MODE clone cleanup verified: %s absent", clone)
	})
	cfg.ConnConfig.Database = clone
	cfg.MaxConns = 4
	pool, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("connect private UUID clone")
	}
	var actualDatabase string
	if err := pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actualDatabase); err != nil || actualDatabase != clone {
		t.Fatal("refusing to initialize anything except the fresh private UUID clone")
	}
	t.Logf("PRE-MODE private UUID clone: %s", clone)
	// The approved template is a narrow historical fixture. Reset only this
	// fresh clone, then install the existing default embedded baseline schema.
	nativeIngestSQL(t, pool, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
	if err := database.RunMigrations(t.Context(), pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("initialize PRE-MODE clone with existing baseline migrations: %v", err)
	}

	r := storagesource.NewRepository(pool)
	source, err := r.CreateSource(t.Context(), storagesource.SourceConfig{
		PluginID: "fixture", ProviderSourceID: "books", RootEntryID: "root",
		ConfigurationRevision: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create PRE-MODE fixture source: %v", err)
	}
	var folderID int
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebooks','Retention RED PRE-MODE') RETURNING id").Scan(&folderID); err != nil {
		t.Fatalf("create PRE-MODE fixture folder: %v", err)
	}
	binding, err := r.Bind(t.Context(), source.Key, folderID)
	if err != nil {
		t.Fatalf("bind PRE-MODE fixture source: %v", err)
	}
	_, file := nativeIngestInput(t)
	x := &nativeIngestFixture{
		s: &Scanner{fileRepo: NewFileRepository(pool), itemRepo: catalog.NewItemRepository(pool), personRepo: catalog.NewPersonRepository(pool)},
		r: r, pool: pool, folder: &models.MediaFolder{ID: folderID, Type: "ebooks"},
		source: source, binding: binding, file: file,
	}
	x.discover(t, "v1")
	return x
}
