//go:build integration

package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/jackc/pgx/v5"
)

func TestNativeOnboardingLocalControlsDB(t *testing.T) {
	pool := metadataCurrentDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	user, profile, store := metadataCurrentProfile(t, pool)
	root := t.TempDir()
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	for name, content := range map[string]string{
		"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OPS/content.opf":        `<package><metadata><title>Ordinary Local Control</title><creator>Local Author</creator><language>en</language><identifier>ISBN: 9780140328721</identifier><meta name="calibre:series" content="Local Series"/><meta name="calibre:series_index" content="2"/></metadata></package>`,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "local.epub")
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var folder int
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebook','Ordinary local control',bloem_platform_resource_owner_id()) RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	metadataExec(t, pool, "INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,$2)", folder, root)
	scan := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0)
	model := &models.MediaFolder{ID: folder, Type: "ebook", Enabled: true, Paths: []string{root}}
	if err := scan.ScanEbookFolder(t.Context(), model); err != nil {
		t.Fatal("actual ordinary scan with unrelated native", err)
	}
	var file int
	var key string
	if err := pool.QueryRow(t.Context(), "SELECT id,content_id FROM media_files WHERE file_path=$1", path).Scan(&file, &key); err != nil {
		t.Fatal(err)
	}
	if key == native {
		t.Fatal("ordinary scan joined native identity")
	}
	if err := scan.ScanEbookFolder(t.Context(), model); err != nil {
		t.Fatal("actual ordinary rescan", err)
	}
	items := catalog.NewItemRepository(pool)
	item, err := items.GetByID(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	item.Title = "Enriched local title"
	item.PosterPath = "local/control-cover.jpg"
	if err = items.Upsert(t.Context(), item); err != nil {
		t.Fatal("same-ID local scalar/artwork enrichment", err)
	}
	// Higher isolation same-key metadata/progress/collection DELETE+INSERT
	// changes data, not the existing identity. No blanket RC restriction.
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(t.Context(), "UPDATE media_items SET overview='Local control' WHERE content_id=$1", key); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(t.Context(), "INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress) VALUES($1,$2,$3,$4,'chapter-2',0.4) ON CONFLICT(user_id,profile_id,content_id) DO UPDATE SET progress=0.5", user, profile, key, file); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(t.Context(), "DELETE FROM media_item_provider_ids WHERE content_id=$1", key); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(t.Context(), "INSERT INTO media_item_provider_ids(content_id,item_type,provider,provider_id) VALUES($1,'ebook','isbn','9780140328721')", key); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Actual presence writer and membership/orphan reconciliation remain legal.
	if err = scanner.NewFileRepository(pool).MarkMissing(t.Context(), file, time.Now()); err != nil {
		t.Fatal(err)
	}
	removed, deleted, _, err := catalog.NewLibraryItemRepository(pool).ReconcileFolderMembership(t.Context(), folder, nil)
	if err != nil || removed != 1 || deleted != 1 {
		t.Fatalf("actual ordinary member/orphan cleanup got %d/%d %v", removed, deleted, err)
	}
	if err = store.DeleteProfile(t.Context(), profile); err != nil {
		t.Fatal("actual profile cleanup", err)
	}
	if err = auth.NewUserRepository(pool).Delete(t.Context(), user); err != nil {
		t.Fatal("actual account cleanup", err)
	}
	var count int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM media_items WHERE content_id=$1", native).Scan(&count); err != nil || count != 1 {
		t.Fatal("ordinary cleanup touched native item", err)
	}
}
