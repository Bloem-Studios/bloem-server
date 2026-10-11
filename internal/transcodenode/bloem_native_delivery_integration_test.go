package transcodenode_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// The worker fixture's byte authority is synthetic, but generated delivery
// retains a real opened source owner through the response without reading it.
type integrationNativeDelivery struct {
	ctx      context.Context
	cancel   context.CancelFunc
	file     mediasource.File
	closed   *atomic.Int32
	once     sync.Once
	closeErr error
}

func openIntegrationNativeDelivery(ctx context.Context, path string, closed *atomic.Int32) (transcodenode.NativeDeliveryAuthority, error) {
	lifetime, cancel := context.WithCancel(ctx)
	file, err := mediasource.OpenLocal(lifetime, path)
	if err != nil {
		cancel()
		return nil, err
	}
	return &integrationNativeDelivery{ctx: lifetime, cancel: cancel, file: file, closed: closed}, nil
}
func (f *integrationNativeDelivery) Reauthorize(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.ctx.Err()
}
func (f *integrationNativeDelivery) Cancel() { f.cancel() }
func (f *integrationNativeDelivery) Close() error {
	f.once.Do(func() {
		f.cancel()
		f.closeErr = f.file.Close()
		// OpenLocal's cancellation hook may already have closed the descriptor.
		// That expected duplicate close is not a source-authority failure.
		if errors.Is(f.closeErr, os.ErrClosed) {
			f.closeErr = nil
		}
		f.closed.Add(1)
	})
	return f.closeErr
}
