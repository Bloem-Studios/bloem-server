package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/models"
)

type nativeContinuationIngester struct{ reached chan context.Context }

func (f *nativeContinuationIngester) IngestFolder(ctx context.Context, _ *models.MediaFolder) (*libraryingest.Result, error) {
	f.reached <- ctx
	return nil, context.Canceled
}
func (f *nativeContinuationIngester) IngestSubtree(ctx context.Context, _ *models.MediaFolder, _ string) (*libraryingest.Result, error) {
	f.reached <- ctx
	return nil, context.Canceled
}
func (f *nativeContinuationIngester) IngestFile(ctx context.Context, _ *models.MediaFolder, _ string) (*libraryingest.Result, error) {
	f.reached <- ctx
	return nil, context.Canceled
}
func (f *nativeContinuationIngester) CancelLibrary(int) int { return 0 }

func TestNativeMutationContinuationLifetimeUnit(t *testing.T) {
	request, cancelRequest := context.WithCancel(context.Background())
	origin := catalog.WithNativePhaseAuthorizer(request, nil, func(context.Context, catalog.NativePhaseQuery, catalog.NativePhaseTargets) error {
		return &catalog.NativePhaseRefusal{Code: "forbidden"}
	})
	life, cancelLife := context.WithCancel(context.Background())
	defer cancelLife()
	continuation := nativeMutationContinuation(origin, life)
	cancelRequest()
	if !catalog.NativePhaseRequest(continuation) || continuation.Err() != nil {
		t.Fatal("origin lost or HTTP cancellation leaked")
	}
	if nativeMutationContinuation(context.Background(), life) != life {
		t.Fatal("trusted no-origin lifecycle changed")
	}
	cancelLife()
	if continuation.Err() != context.Canceled {
		t.Fatal("owning lifecycle cancellation lost")
	}
}

// Exercise all three real worker methods, replacing only the ingest boundary.
func TestNativeMutationDirectScanWorkersCarryOriginUnit(t *testing.T) {
	for _, marked := range []bool{false, true} {
		for _, mode := range []string{"folder", "subtree", "file"} {
			t.Run(mode+map[bool]string{false: "/trusted", true: "/request"}[marked], func(t *testing.T) {
				request, cancel := context.WithCancel(context.Background())
				if marked {
					request = catalog.WithNativePhaseAuthorizer(request, nil, nil)
				}
				life, stop := context.WithCancel(context.Background())
				defer stop()
				reached := make(chan context.Context, 1)
				h := &LibraryHandler{appCtx: life, ingester: &nativeContinuationIngester{reached: reached}}
				folder := &models.MediaFolder{ID: 42, Name: "Synthetic"}
				switch mode {
				case "folder":
					h.runFolderScanAsync(request, "scan", folder, "unit")
				case "subtree":
					h.runSubtreeScanAsync(request, "scan", folder, "/synthetic", "unit")
				case "file":
					h.runFileScanAsync(request, "scan", folder, "/synthetic/file", "unit")
				}
				cancel()
				select {
				case ctx := <-reached:
					if catalog.NativePhaseRequest(ctx) != marked || ctx.Err() != nil {
						t.Fatal("worker origin/lifecycle mismatch")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("worker did not reach ingester")
				}
			})
		}
	}
}
