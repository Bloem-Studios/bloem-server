package nativestorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	// maxConcurrentCovers bounds cover reads per node; a cover takes tens of
	// milliseconds, so this serves a few hundred covers a second.
	maxConcurrentCovers = 16
	// MaxCoverBytes bounds one cover read through a storage plugin.
	MaxCoverBytes = 16 << 20
	// coverReadTimeout bounds a shared read: callers waiting on it keep their
	// own deadlines, but the read itself must not outlive a cancelled leader.
	coverReadTimeout = 30 * time.Second
)

// CoverCache holds covers read through storage plugins, keyed by content ID
// and cover revision, so a cover is never served stale. It is a cache: a miss
// or a failure only costs a read from the source.
type CoverCache interface {
	Get(ctx context.Context, key string) ([]byte, error) // nil, nil on a miss
	Set(ctx context.Context, key string, data []byte) error
}

// coverReader serves storage covers for every listener on a node.
type coverReader struct {
	// coordinator is the router's, so covers open under the same mutation
	// fence and checks as ebook reads.
	coordinator atomic.Pointer[Coordinator]
	cache       atomic.Pointer[CoverCache]
	// slots bounds concurrent reads from sources, so a page of covers cannot
	// flood a provider (nil means unbounded); flights share one read among
	// concurrent requests.
	slots   chan struct{}
	flights singleflight.Group
}

// SetCoverReader publishes the coordinator that opens storage covers.
func (h *Host) SetCoverReader(c *Coordinator) {
	if h != nil {
		h.covers.coordinator.Store(c)
	}
}

// SetCoverCache shares read covers between requests and nodes.
func (h *Host) SetCoverCache(cache CoverCache) {
	if h != nil && cache != nil {
		h.covers.cache.Store(&cache)
	}
}

// ReadCover returns a book's storage cover at revision, from the cover cache
// or through its source. A cached cover was authorized when it was read; the
// signed URL that names it is the request's authority, as for stored artwork.
func (h *Host) ReadCover(ctx context.Context, contentID, revision string) ([]byte, error) {
	if h == nil || h.covers.coordinator.Load() == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	r := &h.covers
	key := "bloem:storage-cover:" + contentID + ":" + revision
	cache := r.cacheOrNil()
	if cache != nil {
		if data, err := cache.Get(ctx, key); err != nil {
			slog.DebugContext(ctx, "storage cover cache read failed", "component", "nativestorage", "error", err)
		} else if data != nil {
			return data, nil
		}
	}
	result := r.flights.DoChan(key, func() (any, error) {
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coverReadTimeout)
		defer cancel()
		data, err := r.read(readCtx, contentID, revision)
		if err == nil && cache != nil {
			if err := cache.Set(readCtx, key, data); err != nil {
				slog.DebugContext(ctx, "storage cover cache write failed", "component", "nativestorage", "error", err)
			}
		}
		return data, err
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-result:
		if res.Err != nil {
			return nil, res.Err
		}
		data, ok := res.Val.([]byte)
		if !ok {
			return nil, storagesource.ErrSourceUnavailable
		}
		return data, nil
	}
}

func (r *coverReader) cacheOrNil() CoverCache {
	if c := r.cache.Load(); c != nil {
		return *c
	}
	return nil
}

func (r *coverReader) read(ctx context.Context, contentID, revision string) ([]byte, error) {
	if r.slots != nil {
		select {
		case r.slots <- struct{}{}:
			defer func() { <-r.slots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	file, err := r.coordinator.Load().OpenCover(ctx, contentID, revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if file.Info().Size > MaxCoverBytes {
		return nil, ErrCoverTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxCoverBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxCoverBytes {
		return nil, ErrCoverTooLarge
	}
	return data, nil
}

// ErrCoverTooLarge reports a cover over MaxCoverBytes.
var ErrCoverTooLarge = fmt.Errorf("storage cover exceeds %d bytes", MaxCoverBytes)

// RedisCoverCache keeps covers in a Redis that may evict them: a dedicated
// instance with a memory limit and an LRU policy, never the server's main
// Redis, whose sessions and grants must not be evicted.
type RedisCoverCache struct {
	client *redis.Client
	ttl    time.Duration
}

func NewRedisCoverCache(client *redis.Client, ttl time.Duration) *RedisCoverCache {
	return &RedisCoverCache{client: client, ttl: ttl}
}

func (c *RedisCoverCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return data, err
}

func (c *RedisCoverCache) Set(ctx context.Context, key string, data []byte) error {
	return c.client.Set(ctx, key, data, c.ttl).Err()
}
