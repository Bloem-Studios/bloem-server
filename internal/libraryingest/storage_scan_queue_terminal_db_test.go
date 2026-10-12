//go:build integration

package libraryingest_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanqueue"
)

type task4QueueIngester struct {
	fixture  *libraryingest.Task4NativeFixture
	returned chan error
	mode     string
	t        *testing.T
}

func (i *task4QueueIngester) IngestFolder(ctx context.Context, folder *models.MediaFolder) (*libraryingest.Result, error) {
	if i.mode != "parent-cancel" {
		once := false
		ctx = libraryingest.WithProgressReporter(ctx, func(update libraryingest.ProgressUpdate) {
			if update.Phase != "processing" || once {
				return
			}
			once = true
			var job context.Context
			select {
			case job = <-i.fixture.JobContexts:
			case <-time.After(10 * time.Second):
				i.t.Fatal("job context not observed")
			}
			if i.mode == "renewal" {
				if _, err := i.fixture.Pool.Exec(ctx, `UPDATE plugin_installations SET enabled=false WHERE id=$1`, *i.fixture.Source.InstallationID); err != nil {
					i.t.Fatal(err)
				}
			} else {
				libraryingest.Task4KillProcessForTest(i.t, i.fixture)
			}
			select {
			case <-job.Done():
			case <-time.After(10 * time.Second):
				i.t.Fatal("internal failure did not stop job")
			}
		})
	}
	r, err := i.fixture.Scanner.ScanStorageFolder(ctx, folder, i.fixture.Location)
	i.returned <- err
	return r, err
}
func (*task4QueueIngester) IngestSubtree(context.Context, *models.MediaFolder, string) (*libraryingest.Result, error) {
	return nil, errors.New("unexpected subtree")
}
func (*task4QueueIngester) IngestFile(context.Context, *models.MediaFolder, string) (*libraryingest.Result, error) {
	return nil, errors.New("unexpected file")
}

func TestStorageScanQueueTerminalCausesDB(t *testing.T) {
	binary := libraryingest.Task4NativeArtifact(t)
	for _, mode := range []string{"renewal", "process", "parent-cancel"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := "list-metadata-gate"

			f := libraryingest.Task4NewNativeFixture(t, binary, fixtureMode)
			if mode == "renewal" {
				libraryingest.Task4FastRenewalForTest(f)
			}
			if mode != "parent-cancel" {
				if err := os.WriteFile(f.Notify+".release", []byte("release"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			repo := scanqueue.NewRepository(f.Pool)
			ingester := &task4QueueIngester{fixture: f, returned: make(chan error, 1), mode: mode, t: t}
			app, cancel := context.WithCancel(t.Context())
			defer cancel()
			service := scanqueue.NewService(repo, catalog.NewFolderRepository(f.Pool), ingester, nil, app, 1, 1)
			defer service.Stop()
			if ok, err := service.EnqueueLibraryScan(t.Context(), f.Folder.ID, "task4-terminal-fixture"); err != nil || !ok {
				t.Fatalf("enqueue %v %v", ok, err)
			}
			run, err := repo.GetActiveByScope(t.Context(), f.Folder.ID, scanqueue.ModeLibrary, "")
			if err != nil {
				t.Fatal(err)
			}
			service.Start()
			libraryingest.Task4WaitProvider(t, f.Notify)
			switch mode {
			case "parent-cancel":
				if _, err = service.CancelByLibrary(t.Context(), f.Folder.ID); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-ingester.returned:
				if err == nil {
					t.Fatal("failure returned success")
				}
				if mode == "parent-cancel" && !errors.Is(err, context.Canceled) {
					t.Errorf("caller cancellation lost: %v", err)
				}
				if mode != "parent-cancel" && errors.Is(err, context.Canceled) {
					t.Errorf("internal failure contains generic cancellation: %v", err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("native fixture did not finish")
			}
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
			for {
				actual, err := repo.GetByID(t.Context(), run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if actual.Status == scanqueue.StatusFailed || actual.Status == scanqueue.StatusCancelled || actual.Status == scanqueue.StatusCompleted {
					want := scanqueue.StatusFailed
					if mode == "parent-cancel" {
						want = scanqueue.StatusCancelled
					}
					if actual.Status != want {
						t.Errorf("queue terminal state=%s want=%s", actual.Status, want)
					}
					t.Logf("native %s queue state=%s", mode, actual.Status)
					break
				}
				select {
				case <-deadline.C:
					t.Fatal("queue terminal state missing")
				case <-poll.C:
				}
			}
		})
	}
}
