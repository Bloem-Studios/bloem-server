package libraryingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// These dispatch tests own exactly library 42. A fixture verdict never
// classifies an unlisted library or derives durable mode from caller input.
type nativeDispatchFolderReader struct {
	folder models.MediaFolder
	native bool
}

func (f *nativeDispatchFolderReader) NativeScanFolder(ctx context.Context, id int) (*models.MediaFolder, bool, error) {
	if ctx == nil || f == nil || id != 42 || f.folder.ID != 42 {
		return nil, false, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	folder := f.folder
	return &folder, f.native, nil
}

func (f *nativeDispatchFolderReader) UpdateLastScanned(ctx context.Context, id int, _ time.Time) error {
	_, _, err := f.NativeScanFolder(ctx, id)
	return err
}

func newNativeDispatchExecutor() (*Executor, *nativeDispatchFolderReader) {
	folders := &nativeDispatchFolderReader{
		folder: models.MediaFolder{ID: 42, Type: "ebook", Enabled: true},
		native: true,
	}
	return NewExecutor(nil, nil, folders, nil, nil, nil), folders
}

type nativeIngestFixture struct {
	bound     bool
	lookupErr error
	started   chan struct{}
}

func (f *nativeIngestFixture) HasNativeBinding(context.Context, int) (bool, error) {
	return f.bound, f.lookupErr
}
func (f *nativeIngestFixture) IngestNativeFolder(ctx context.Context, _ *models.MediaFolder) (*Result, error) {
	if f.started != nil {
		close(f.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &Result{ScanResult: &scanner.ScanResult{New: 1}}, nil
}
func TestNativeStorageDispatchBeforeFilesystemRequirements(t *testing.T) {
	e, _ := newNativeDispatchExecutor()
	e.SetNativeIngestor(&nativeIngestFixture{bound: true})
	result, err := e.IngestFolder(t.Context(), &models.MediaFolder{ID: 42, Type: "ebooks"})
	if err != nil || result == nil || result.ScanResult.New != 1 {
		t.Fatalf("native ingest incorrectly reached filesystem requirements: %v", err)
	}
}
func TestNativeStorageDispatchPreservesLocalAndLookupErrors(t *testing.T) {
	e, folders := newNativeDispatchExecutor()
	folders.native = false
	e.SetNativeIngestor(&nativeIngestFixture{})
	if _, err := e.IngestFolder(t.Context(), &models.MediaFolder{ID: 42, Type: "ebooks"}); err == nil {
		t.Fatal("local executor requirements bypassed")
	}
	folders.native = true
	denied := errors.New("source lookup unavailable")
	e.SetNativeIngestor(&nativeIngestFixture{lookupErr: denied})
	if _, err := e.IngestFolder(t.Context(), &models.MediaFolder{ID: 42, Type: "ebooks"}); !errors.Is(err, denied) {
		t.Fatalf("native lookup error fell back to local: %v", err)
	}
}
func TestNativeStorageDispatchRejectsMixedAndPartialScopes(t *testing.T) {
	e, folders := newNativeDispatchExecutor()
	e.SetNativeIngestor(&nativeIngestFixture{bound: true})
	folder := &models.MediaFolder{ID: 42, Type: "ebook", Enabled: true}
	for _, durable := range []models.MediaFolder{
		{ID: 42, Type: "movies", Enabled: true},
		{ID: 42, Type: "ebook", Enabled: true, Paths: []string{"/local"}},
	} {
		folders.folder = durable
		if _, err := e.IngestFolder(t.Context(), folder); err == nil {
			t.Fatal("unsupported native/mixed library accepted")
		}
	}
	folders.folder = *folder
	if _, err := e.IngestFile(t.Context(), folder, "bloem-storage:opaque/../book"); err == nil {
		t.Fatal("native opaque file sent through local normalization")
	}
	if _, err := e.IngestSubtree(t.Context(), folder, "bloem-storage:opaque"); err == nil {
		t.Fatal("native partial scope accepted without native scope authority")
	}
}
func TestNativeStorageDispatchCancelLibrary(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	e, _ := newNativeDispatchExecutor()
	f := &nativeIngestFixture{bound: true, started: make(chan struct{})}
	e.SetNativeIngestor(f)
	result := make(chan error, 1)
	go func() { _, err := e.IngestFolder(ctx, &models.MediaFolder{ID: 42, Type: "ebooks"}); result <- err }()
	select {
	case <-f.started:
	case <-ctx.Done():
		t.Fatal("native scan did not start")
	}
	if n := e.CancelLibrary(42); n != 1 {
		t.Fatalf("cancel did not track native scan: %d", n)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled native result: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("cancel did not stop native scan")
	}
}
