package nativestorage

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

type coverWaitRegistry struct {
	snapshot *plugins.NativeStorageSnapshot
}

func (s coverWaitRegistry) Snapshot(context.Context, uuid.UUID, uuid.UUID) (*plugins.NativeStorageSnapshot, error) {
	return clonePlaybackSnapshot(s.snapshot), nil
}

type coverWaitCache struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (s *coverWaitCache) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key], nil
}
func (s *coverWaitCache) Set(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = data
	return nil
}

type coverWaitSource struct {
	*sourceStub
	release <-chan struct{}
	reads   atomic.Int32
}

func (s *coverWaitSource) ReadRange(ctx context.Context, r mediasource.Ref, offset, length int64, w io.Writer) error {
	s.reads.Add(1)
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.sourceStub.ReadRange(ctx, r, offset, length, w)
}

func TestEveryCoalescedCoverWaiterReauthorizesAfterWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, refs, registry, run, _ := fixture(t)
		c.Registry = coverWaitRegistry{registry.snapshot}
		release := make(chan struct{})
		source := &coverWaitSource{sourceStub: run.source, release: release}
		c.Runtime = readAuthorityRuntime{Lease{Context: context.Background(), Source: source, Description: run.description}}
		h := &Host{}
		h.SetCoverReader(c)
		h.SetCoverCache(&coverWaitCache{data: map[string][]byte{}})
		rev := artworkkey.StorageCoverRevision("cover/book", "cover:1")
		var revoked atomic.Bool
		leader := WithCoverReadAuthority(t.Context(), refs.cover, func(context.Context) (storagesource.CoverReference, error) { return refs.cover, nil })
		waiter := WithCoverReadAuthority(t.Context(), refs.cover, func(context.Context) (storagesource.CoverReference, error) {
			if revoked.Load() {
				return storagesource.CoverReference{}, storagesource.ErrReferenceConflict
			}
			return refs.cover, nil
		})
		type result struct {
			data []byte
			err  error
		}
		first := make(chan result, 1)
		second := make(chan result, 1)
		go func() { data, err := h.ReadCover(leader, "book", rev); first <- result{data, err} }()
		synctest.Wait() // provider read is blocked
		go func() { data, err := h.ReadCover(waiter, "book", rev); second <- result{data, err} }()
		synctest.Wait() // both callers now wait on the same flight
		revoked.Store(true)
		close(release)
		a, b := <-first, <-second
		if string(a.data) != "book" || a.err != nil {
			t.Fatalf("authorized leader: %q %v", a.data, a.err)
		}
		if b.data != nil || !errors.Is(b.err, storagesource.ErrReferenceConflict) {
			t.Fatalf("revoked waiter got bytes: %q %v", b.data, b.err)
		}
		if source.reads.Load() != 1 {
			t.Fatalf("cover reads=%d, want one shared read", source.reads.Load())
		}
	})
}
