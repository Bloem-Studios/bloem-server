package scanqueue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// storageOutcomeIngestor makes the test's library a storage library whose
// scan ends as scan decides.
type storageOutcomeIngestor struct {
	folderID int
	scan     func(context.Context) error
}

func (s storageOutcomeIngestor) FolderLocation(_ context.Context, id int) (storagesource.Location, bool, error) {
	return storagesource.Location{FolderID: id, SourceKey: uuid.New()}, id == s.folderID, nil
}

func (s storageOutcomeIngestor) ScanStorageFolder(ctx context.Context, _ *models.MediaFolder, _ storagesource.Location) (*libraryingest.Result, error) {
	return &libraryingest.Result{}, s.scan(ctx)
}

// A storage scan's internal failures often carry context.Canceled from the
// teardown of provider reads. Through this unchanged queue they must end
// failed with an error message, while an operator cancel ends cancelled.
func TestStorageScanOutcomeReachesScanRun(t *testing.T) {
	ctx, _, repo, folderID := openDirectRunTestRepository(t)
	folder := &models.MediaFolder{ID: folderID, Type: "movies", Enabled: true}
	start := func(t *testing.T) *models.ScanRun {
		t.Helper()
		run, created, err := repo.Create(ctx, CreateInput{LibraryID: folderID, Mode: ModeLibrary, Trigger: "manual"})
		if err != nil || !created {
			t.Fatalf("create run: created=%v err=%v", created, err)
		}
		if run, err = repo.Start(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		return run
	}
	t.Run("internal failure", func(t *testing.T) {
		executor := libraryingest.NewExecutor(nil, nil, nil, nil, nil, nil)
		executor.SetStorageIngestor(storageOutcomeIngestor{folderID: folderID, scan: func(context.Context) error {
			return errors.Join(errors.New("native media probe failed"), context.Canceled)
		}})
		svc := NewService(repo, overlapTestFolders{folder: folder}, executor, nil, context.Background(), 1, 1)
		run := start(t)
		svc.process(run)
		got, err := repo.GetByID(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusFailed || got.ErrorMessage != "storage scan failed: native media probe failed; context canceled" {
			t.Fatalf("internal failure ended %s with message %q", got.Status, got.ErrorMessage)
		}
	})
	t.Run("operator cancel", func(t *testing.T) {
		started := make(chan struct{})
		executor := libraryingest.NewExecutor(nil, nil, nil, nil, nil, nil)
		executor.SetStorageIngestor(storageOutcomeIngestor{folderID: folderID, scan: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			// A provider can report the cancellation as its own status.
			return status.Error(codes.Canceled, "provider stream canceled")
		}})
		svc := NewService(repo, overlapTestFolders{folder: folder}, executor, nil, context.Background(), 1, 1)
		run := start(t)
		done := make(chan struct{})
		go func() { defer close(done); svc.process(run) }()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("storage scan did not start")
		}
		svc.runningMu.Lock()
		for _, running := range svc.runningCancels {
			running.cancel()
		}
		svc.runningMu.Unlock()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("canceled storage scan did not finish")
		}
		got, err := repo.GetByID(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusCancelled || strings.TrimSpace(got.ErrorMessage) != "" {
			t.Fatalf("operator cancel ended %s with message %q", got.Status, got.ErrorMessage)
		}
	})
}
