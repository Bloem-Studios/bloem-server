package libraryingest

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// followingPage is the next page of the ebook directory being listed, listed
// and prepared while the current page is applied. It is used only if the
// journal then stands at its checkpoint; otherwise that page is listed again.
type followingPage struct {
	checkpoint storagesource.Checkpoint
	cancel     context.CancelFunc
	done       chan struct{}
	page       *storagev1.ListResponse
	books      []scanner.StorageEbook
	skipped    int
	err        error
}

func (c *StorageScanner) fetchFollowing(ctx context.Context, lease storagesource.Lease, checkpoint storagesource.Checkpoint, next string, source storagesource.SourceConfig, provider storagev1.StorageProviderClient, media mediasource.Source) *followingPage {
	ctx, cancel := context.WithCancel(ctx)
	f := &followingPage{checkpoint: storagesource.Checkpoint{DirectoryID: checkpoint.DirectoryID, Cursor: next}, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		f.page, f.err = c.sources.FetchFollowingPage(ctx, lease, checkpoint, next, provider)
		if f.err == nil {
			f.books, f.skipped, f.err = c.prepare(ctx, media, source, f.page.GetEntries())
		}
	}()
	return f
}

// take waits for the page and returns it if it was listed for checkpoint.
func (f *followingPage) take(checkpoint storagesource.Checkpoint) (*storagev1.ListResponse, []scanner.StorageEbook, int, bool) {
	if f == nil || f.checkpoint != checkpoint {
		return nil, nil, 0, false
	}
	<-f.done
	return f.page, f.books, f.skipped, f.err == nil
}

// stop ends a listing still in progress and waits for it.
func (f *followingPage) stop() {
	if f != nil {
		f.cancel()
		<-f.done
	}
}
