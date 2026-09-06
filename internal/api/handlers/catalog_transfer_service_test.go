package handlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminjob"
	"github.com/Silo-Server/silo-server/internal/catalogseed"
	"github.com/Silo-Server/silo-server/internal/s3client"
	"github.com/jackc/pgx/v5/pgxpool"
)

type catalogTransferStore struct {
	signed int
	expiry time.Duration
}

func (*catalogTransferStore) Bucket() string { return "fixture" }
func (s *catalogTransferStore) PresignGetURL(_ context.Context, _, _ string, expiry time.Duration) (string, error) {
	s.signed++
	s.expiry = expiry
	return "https://example.invalid/seed.json.gz", nil
}
func (*catalogTransferStore) GetObject(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("unexpected storage read")
}
func (*catalogTransferStore) UploadFile(context.Context, string, string, string, string) (int64, error) {
	return 0, errors.New("unexpected upload")
}
func (*catalogTransferStore) ListObjectInfos(context.Context, string, string) ([]s3client.ObjectInfo, error) {
	return nil, nil
}
func (*catalogTransferStore) DeleteObject(context.Context, string, string) error { return nil }
func (*catalogTransferStore) MakeObjectPublic(context.Context, string, string) error {
	return errors.New("ACL changes are forbidden in this test")
}
func (*catalogTransferStore) PublicURL(string, string) (string, error) {
	return "", errors.New("public ACL URL is forbidden in this test")
}

func catalogTransferPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
func TestCatalogTransferPersistsJobsAndSignedLink(t *testing.T) {
	pool := catalogTransferPool(t)
	repo := adminjob.NewRepository(pool)
	store := &catalogTransferStore{}
	h := NewCatalogSeedHandler(nil, repo, store)
	export, err := h.CreateCatalogExportJob(t.Context(), 1, catalogseed.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM admin_jobs WHERE id=$1`, export.ID) })
	saved, err := repo.GetByID(t.Context(), export.ID)
	if err != nil || saved.Status != adminjob.StatusQueued {
		t.Fatalf("unpersisted job: %#v %v", saved, err)
	}
	if _, err := h.CreateCatalogExportJob(t.Context(), 1, catalogseed.ExportOptions{}); !errors.Is(err, adminjob.ErrActiveJobConflict) {
		t.Fatalf("duplicate queue: %v", err)
	}
	if _, err := h.PublishCatalogExportJob(t.Context(), export.ID); err == nil {
		t.Fatal("published queued job")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE admin_jobs SET status='completed',artifact_bucket='fixture',artifact_key='seed.json.gz' WHERE id=$1`, export.ID); err != nil {
		t.Fatal(err)
	}
	first, err := h.PublishCatalogExportJob(t.Context(), export.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.PublishCatalogExportJob(t.Context(), export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if store.signed != 1 || store.expiry != 7*24*time.Hour || first.PublicURL != second.PublicURL || !first.PublishedAt.Equal(*second.PublishedAt) {
		t.Fatalf("link renewed: %#v %#v", first, second)
	}
	path := filepath.Join(t.TempDir(), "seed.json.gz")
	if err := os.WriteFile(path, []byte("worker reads later"), 0600); err != nil {
		t.Fatal(err)
	}
	job, err := h.CreateCatalogImportJob(t.Context(), 1, CatalogImportSourceSelection{LocalPath: path}, catalogseed.ImportOptions{ConflictMode: catalogseed.ConflictModeSkipExisting})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM admin_jobs WHERE id=$1`, job.ID) })
	saved, err = repo.GetByID(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var request adminjob.CatalogImportRequest
	if err := json.Unmarshal(saved.RequestPayload, &request); err != nil {
		t.Fatal(err)
	}
	if request.LocalPath != path || saved.Status != adminjob.StatusQueued || saved.CompletedAt != nil {
		t.Fatalf("unexpected queued import: %#v", saved)
	}
}

func TestCatalogTransferSynchronousImportCommitsBeforeReturning(t *testing.T) {
	pool := catalogTransferPool(t)
	h := NewCatalogSeedHandler(catalogseed.NewService(pool, nil, nil), nil, nil)
	root := t.TempDir()
	name := "catalog-transfer-" + filepath.Base(root)
	bundle := catalogseed.Bundle{Manifest: catalogseed.Manifest{FormatVersion: catalogseed.CurrentBundleVersion}, Libraries: []catalogseed.LibraryRecord{{ExportedID: 1, Paths: []string{root}, Type: "movies", Name: name, Enabled: true}}}
	var data bytes.Buffer
	writer := gzip.NewWriter(&data)
	if err := json.NewEncoder(writer).Encode(bundle); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "seed.json.gz")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM media_folders WHERE name=$1`, name) })
	result, err := h.ImportCatalog(t.Context(), CatalogImportSourceSelection{LocalPath: path}, catalogseed.ImportOptions{ConflictMode: catalogseed.ConflictModeSkipExisting})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_folders WHERE name=$1`, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if result.LibrariesCreated != 1 || count != 1 {
		t.Fatalf("import returned before persistence: %#v count=%d", result, count)
	}
}
