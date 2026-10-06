//go:build integration

package catalog_test

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/imagecache"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nativeCoverFile struct {
	*bytes.Reader
	info mediasource.Info
}

func (f *nativeCoverFile) Info() mediasource.Info { return f.info }
func (f *nativeCoverFile) Close() error           { return nil }
func nativeCoverEPUB(t *testing.T) []byte {
	t.Helper()
	var cover, book bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 32, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 5), 120, 255})
		}
	}
	if err := png.Encode(&cover, img); err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(&book)
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{"META-INF/container.xml", []byte(`<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`)},
		{"content.opf", []byte(`<package><metadata><title>Readable Cover Book</title><creator>Ada Writer</creator><language>en</language><date>2024-01-02</date></metadata><manifest><item id="cover" href="cover.png" media-type="image/png" properties="cover-image"/></manifest></package>`)},
		{"cover.png", cover.Bytes()},
	} {
		w, err := archive.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return book.Bytes()
}

func TestNativeIngestEmbeddedCoverReadableDB(t *testing.T) {
	pool := nativeIngestDatabase(t)
	ctx := context.Background()
	r := storagesource.NewRepository(pool)
	source, err := r.CreateSource(ctx, storagesource.SourceConfig{PluginID: "fixture", ProviderSourceID: "cover", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var folderID int
	if err = pool.QueryRow(ctx, "INSERT INTO media_folders(type,name) VALUES('ebooks','Task4 cover') RETURNING id").Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	binding, err := r.Bind(ctx, source.Key, folderID)
	if err != nil {
		t.Fatal(err)
	}
	data := nativeCoverEPUB(t)
	file := &nativeCoverFile{Reader: bytes.NewReader(data), info: mediasource.Info{Name: "book.epub", LogicalPath: "Books/book.epub", Revision: "cover-v1", Size: int64(len(data))}}
	run, err := r.Begin(ctx, source.Key, "cover-discovery", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cp, ok, err := r.NextDirectory(ctx, run)
	if err != nil || !ok {
		t.Fatal(err)
	}
	entry := &storagev1.Entry{Id: "book", Name: file.info.Name, LogicalPath: file.info.LogicalPath, Revision: file.info.Revision, Size: file.info.Size, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
	if err = r.ApplyPage(ctx, run, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{entry}, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(ctx, run); err != nil {
		t.Fatal(err)
	}
	lease, err := r.BeginIngestion(ctx, run.RunID, binding.ID, "cover-ingestion", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := r.NextIngestion(ctx, lease)
	if err != nil || !ok {
		t.Fatal(err)
	}
	store, err := blobstore.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cacher := imagecache.New(blobstore.NewByteStore(store))
	cacher.SetArtworkRevisionTracker(catalog.NewArtworkRevisionTracker(pool))
	s := scanner.NewScanner(scanner.NewFileRepository(pool), "", store, 1, false, 0)
	s.SetImageCacher(cacher)
	// A late checkpoint failure occurs after image upload and all catalog writes.
	nativeIngestSQL(t, pool, "ALTER TABLE bloem_storage_ingestion ADD CONSTRAINT task4_cover_late CHECK(last_entry_id='')")
	if _, err = s.PublishNativeEbook(ctx, r, claim, &models.MediaFolder{ID: folderID, Type: "ebooks"}, file, scanner.NativeEbookSidecars{Complete: true}); err == nil {
		t.Fatal("late cover publication did not fail")
	}
	var files, manifests int
	if err = pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM media_files WHERE media_folder_id=$1),(SELECT count(*) FROM artwork_revision_gc_candidates WHERE original_path LIKE 'local/ebooks/%')", folderID).Scan(&files, &manifests); err != nil || files != 0 || manifests == 0 {
		t.Fatalf("rollback/orphan tracking: %d/%d %v", files, manifests, err)
	}
	again, ok, err := r.NextIngestion(ctx, lease)
	if err != nil || !ok || again.Token != claim.Token {
		t.Fatalf("cover failure advanced claim: %v", err)
	}
	nativeIngestSQL(t, pool, "ALTER TABLE bloem_storage_ingestion DROP CONSTRAINT task4_cover_late")
	id, err := s.PublishNativeEbook(ctx, r, claim, &models.MediaFolder{ID: folderID, Type: "ebooks"}, file, scanner.NativeEbookSidecars{Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err := catalog.NewItemRepository(pool).GetByID(ctx, id)
	if err != nil || !strings.HasPrefix(item.PosterPath, "local/ebooks/") || item.PosterThumbhash == "" {
		t.Fatalf("cover missing %+v %v", item, err)
	}
	object, _, err := store.Get(ctx, item.PosterPath)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	if _, _, err = image.Decode(object); err != nil {
		t.Fatalf("cached cover unreadable: %v", err)
	}
}
func nativeIngestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	secret := "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	info, err := os.Stat(secret)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("task-owned mode-0600 database-url required")
	}
	data, err := os.ReadFile(secret)
	if err != nil {
		t.Fatal("read task DB configuration")
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid task DB configuration")
	}
	template := cfg.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		t.Fatal("refusing non-owned template")
	}
	adminCfg := cfg.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		t.Fatal("connect clone admin")
	}
	name := "bloem_storage_test_task4_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	for _, migration := range []struct{ table, glob string }{{"bloem_storage_sources", "*_bloem_native_storage_sources.sql"}, {"bloem_storage_installations", "*_bloem_native_storage_registry.sql"}, {"bloem_storage_ingestion", "*_bloem_native_storage_ingestion.sql"}} {
		var exists *string
		if err = pool.QueryRow(ctx, "SELECT to_regclass($1)::text", migration.table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != nil {
			continue
		}
		paths, err := filepath.Glob("../../migrations/sql/" + migration.glob)
		if err != nil || len(paths) != 1 {
			t.Fatal("owned migration missing")
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		nativeIngestSQL(t, pool, strings.Split(string(data), "-- +goose Down")[0])
	}
	return pool
}
func nativeIngestSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
