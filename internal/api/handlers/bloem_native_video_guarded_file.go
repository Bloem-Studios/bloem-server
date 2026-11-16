package handlers

import (
	"context"
	"errors"
	"sync"
)

// nativeVideoGuardedFile rechecks a bound playback's authority around every
// read of a locally served source. Cancellation stops source IO; Close
// preserves its joined cleanup and host admission lifetime. Never release an
// owner while its provider read is active.
type nativeVideoGuardedFile struct {
	NativeVideoFile
	ctx      context.Context //nolint:containedctx // the source's own lifetime, which Context exposes
	cancel   context.CancelFunc
	stop     func() bool
	joined   chan struct{}
	check    func(context.Context) error
	mu       sync.Mutex
	sticky   error
	close    sync.Once
	closeErr error
}

func (f *nativeVideoGuardedFile) watchCancellation() {
	f.joined = make(chan struct{})
	f.stop = context.AfterFunc(f.ctx, func() {
		defer close(f.joined)
		f.NativeVideoFile.Cancel()
	})
}

func (f *nativeVideoGuardedFile) Context() context.Context { return f.ctx }
func (f *nativeVideoGuardedFile) Cancel()                  { f.cancel(); f.NativeVideoFile.Cancel() }
func (f *nativeVideoGuardedFile) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return errors.Join(f.sticky, f.NativeVideoFile.Err())
}
func (f *nativeVideoGuardedFile) fail(err error) error {
	if err != nil {
		f.mu.Lock()
		if f.sticky == nil {
			f.sticky = err
		}
		f.mu.Unlock()
		f.Cancel()
	}
	return err
}
func (f *nativeVideoGuardedFile) Reauthorize(ctx context.Context) error {
	if err := f.Err(); err != nil {
		return err
	}
	if err := f.ctx.Err(); err != nil {
		return f.fail(err)
	}
	if err := f.check(ctx); err != nil {
		return f.fail(err)
	}
	if err := f.NativeVideoFile.Reauthorize(ctx); err != nil {
		return f.fail(err)
	}
	return f.fail(f.check(ctx))
}
func (f *nativeVideoGuardedFile) Read(p []byte) (int, error) {
	if err := f.Reauthorize(f.ctx); err != nil {
		clear(p)
		return 0, err
	}
	n, err := f.NativeVideoFile.Read(p)
	if terminal := f.Reauthorize(f.ctx); terminal != nil {
		clear(p)
		return 0, terminal
	}
	return n, err
}
func (f *nativeVideoGuardedFile) ReadAt(p []byte, offset int64) (int, error) {
	if err := f.Reauthorize(f.ctx); err != nil {
		clear(p)
		return 0, err
	}
	n, err := f.NativeVideoFile.ReadAt(p, offset)
	if terminal := f.Reauthorize(f.ctx); terminal != nil {
		clear(p)
		return 0, terminal
	}
	return n, err
}
func (f *nativeVideoGuardedFile) Seek(offset int64, whence int) (int64, error) {
	if err := f.Reauthorize(f.ctx); err != nil {
		return 0, err
	}
	pos, err := f.NativeVideoFile.Seek(offset, whence)
	if terminal := f.Reauthorize(f.ctx); terminal != nil {
		return 0, terminal
	}
	return pos, err
}
func (f *nativeVideoGuardedFile) Close() error {
	f.close.Do(func() {
		if f.stop != nil {
			if !f.stop() {
				<-f.joined
			}
		}
		f.Cancel()
		f.closeErr = f.NativeVideoFile.Close()
	})
	return f.closeErr
}
