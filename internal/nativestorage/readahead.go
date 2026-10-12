package nativestorage

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
)

// ReadAheadSource is an admitted, revision-pinned file whose own reads enforce
// source policy. ReadAt must be safe for concurrent use. Err reports a sticky
// failure or cancellation without I/O; Reauthorize performs a fresh check of
// current authority; Cancel interrupts reads that are in flight.
type ReadAheadSource interface {
	mediasource.File
	Err() error
	Cancel()
	Reauthorize(context.Context) error
}

// ReadAheadConfig is chosen by the trusted consumer, never by a request.
//
// Revocation window: a canceled or failed source (Err) stops delivery at the
// next read. Any other revocation that Reauthorize observes stops delivery
// within AuthorizationWindow, because bytes, cached or not, are only copied
// out while the latest successful Reauthorize began less than
// AuthorizationWindow ago. Every block fetch also passes the source's own
// per-read authority and revision checks.
type ReadAheadConfig struct {
	// BlockBytes is the aligned fetch size; one provider range read per block.
	BlockBytes int
	// CacheBlocks bounds retained and in-flight blocks (memory per reader).
	CacheBlocks int
	// PrefetchBlocks are fetched ahead of a sequential reader, concurrently.
	PrefetchBlocks int
	// AuthorizationWindow is the longest delivery may continue on one check.
	AuthorizationWindow time.Duration
}

const (
	readAheadMinBlock      = 64 << 10
	readAheadMaxBlock      = 8 << 20 // the largest single provider range read
	readAheadMaxCache      = 64 << 20
	readAheadMaxPrefetch   = 8
	readAheadMaxAuthWindow = 10 * time.Second
)

var ErrReadAheadInvalid = errors.New("invalid native read-ahead configuration")

// Validate rejects budgets that could hold unbounded memory or authority.
func (c ReadAheadConfig) Validate() error {
	if c.BlockBytes < readAheadMinBlock || c.BlockBytes > readAheadMaxBlock || c.PrefetchBlocks < 0 || c.PrefetchBlocks > readAheadMaxPrefetch ||
		c.CacheBlocks < c.PrefetchBlocks+1 || int64(c.CacheBlocks)*int64(c.BlockBytes) > readAheadMaxCache ||
		c.AuthorizationWindow <= 0 || c.AuthorizationWindow > readAheadMaxAuthWindow {
		return ErrReadAheadInvalid
	}
	return nil
}

type readAheadBlock struct {
	start, end int64
	data       []byte
	err        error
	ready      bool
	used       uint64
	done       chan struct{}
}

// ReadAhead serves a native file through a small LRU of aligned blocks, so a
// consumer's small reads (http.ServeContent copies 32 KiB at a time) become a
// few large provider range reads, with sequential blocks fetched ahead. It owns
// the source: Close cancels fetches in flight, joins them and closes the source.
type ReadAhead struct {
	ctx    context.Context
	source ReadAheadSource
	info   mediasource.Info
	config ReadAheadConfig

	mu       sync.Mutex
	blocks   map[int64]*readAheadBlock
	tick     uint64
	hint     int64
	next     int64
	inflight int
	closed   bool
	sticky   error
	fetches  sync.WaitGroup
	closing  chan struct{}

	authMu     sync.Mutex
	authorized time.Time

	cursorMu sync.Mutex
	cursor   int64

	closeOnce sync.Once
	closeErr  error
}

// NewReadAhead takes ownership of source only on success. ctx bounds the
// consumer: its cancellation stops delivery and is passed to Reauthorize.
func NewReadAhead(ctx context.Context, source ReadAheadSource, config ReadAheadConfig) (*ReadAhead, error) {
	if ctx == nil || source == nil {
		return nil, ErrReadAheadInvalid
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	info := source.Info()
	if info.Size < 0 {
		return nil, ErrReadAheadInvalid
	}
	return &ReadAhead{ctx: ctx, source: source, info: info, config: config, blocks: make(map[int64]*readAheadBlock), hint: info.Size, next: -1, closing: make(chan struct{})}, nil
}

// RangeEnd returns the exclusive end of a single closed "bytes=a-b" request
// range, or size for every other form. It is only a fetch hint for
// SetRangeEnd; http.ServeContent remains the Range authority.
func RangeEnd(header string, size int64) int64 {
	spec, ok := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return size
	}
	first, last, ok := strings.Cut(spec, "-")
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	if !ok || first == "" || last == "" {
		return size
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return size
	}
	end, err := strconv.ParseInt(last, 10, 64)
	if err != nil || start < 0 || end < start || end >= size {
		return size
	}
	return end + 1
}

// SetRangeEnd tells the reader that its consumer stops at end, so fetches
// that begin below end stop there and nothing is prefetched past it. A read
// beyond end still succeeds; it simply fetches without the hint.
func (r *ReadAhead) SetRangeEnd(end int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hint = min(max(end, 0), r.info.Size)
}

func (r *ReadAhead) Info() mediasource.Info { return r.info }

// Err returns the first failure delivered to a reader.
func (r *ReadAhead) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sticky
}

func (r *ReadAhead) fail(err error) error {
	if err == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sticky == nil {
		r.sticky = err
	}
	return r.sticky
}

func (r *ReadAhead) admit() error {
	r.mu.Lock()
	closed, sticky := r.closed, r.sticky
	r.mu.Unlock()
	if closed {
		return os.ErrClosed
	}
	if sticky != nil {
		return sticky
	}
	if err := r.source.Err(); err != nil {
		return r.fail(err)
	}
	if err := r.ctx.Err(); err != nil {
		return r.fail(err)
	}
	return nil
}

// Reauthorize checks current authority now; a success starts a new window.
func (r *ReadAhead) Reauthorize(ctx context.Context) error {
	if ctx == nil {
		return ErrReadAheadInvalid
	}
	r.authMu.Lock()
	defer r.authMu.Unlock()
	return r.reauthorizeLocked(ctx)
}

// authorize serializes fresh checks; concurrent readers share one result.
func (r *ReadAhead) authorize() error {
	r.authMu.Lock()
	defer r.authMu.Unlock()
	if !r.authorized.IsZero() && time.Since(r.authorized) < r.config.AuthorizationWindow {
		return nil
	}
	return r.reauthorizeLocked(r.ctx)
}

func (r *ReadAhead) reauthorizeLocked(ctx context.Context) error {
	if err := r.admit(); err != nil {
		return err
	}
	// The window starts when the check starts, never when it returns.
	started := time.Now()
	if err := r.source.Reauthorize(ctx); err != nil {
		return r.fail(err)
	}
	r.authorized = started
	return nil
}

// ReadAt follows io.ReaderAt and mediasource.File: a full read returns a nil
// error even at the end of the file. On failure no bytes are committed.
func (r *ReadAhead) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errors.New("negative native read offset")
	}
	if err := r.admit(); err != nil {
		clear(p)
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.info.Size {
		return 0, io.EOF
	}
	want := min(int64(len(p)), r.info.Size-offset)
	for total := int64(0); total < want; {
		n, err := r.readBlock(p[total:want], offset+total)
		if err != nil {
			clear(p)
			return 0, r.fail(err)
		}
		total += n
	}
	if want < int64(len(p)) {
		return int(want), io.EOF
	}
	return int(want), nil
}

// readBlock copies bytes from the block holding offset, fetching it if needed.
func (r *ReadAhead) readBlock(p []byte, offset int64) (int64, error) {
	block, err := r.lookup(offset)
	if err != nil {
		return 0, err
	}
	select {
	case <-block.done:
	case <-r.closing:
		return 0, os.ErrClosed
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
	if block.err != nil {
		return 0, block.err
	}
	if err := r.authorize(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, os.ErrClosed
	}
	n := int64(copy(p, block.data[offset-block.start:]))
	r.tick++
	block.used = r.tick
	r.next = offset + n
	r.mu.Unlock()
	// A stop or failure that raced the copy must not release these bytes.
	if err := r.source.Err(); err != nil {
		clear(p[:n])
		return 0, err
	}
	return n, nil
}

// lookup returns the block holding offset and schedules sequential prefetch.
func (r *ReadAhead) lookup(offset int64) (*readAheadBlock, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, os.ErrClosed
	}
	size, blockBytes := r.info.Size, int64(r.config.BlockBytes)
	index := offset / blockBytes
	sequential := r.next < 0 || offset == r.next
	block := r.blocks[index]
	if block != nil && (offset < block.start || offset >= block.end) {
		block = nil // A clipped or mid-block fetch does not cover this offset.
	}
	if block == nil {
		end := min((index+1)*blockBytes, size)
		if offset < r.hint {
			end = min(end, r.hint)
		}
		r.makeRoomLocked(index, index)
		// A demand fetch starts at the requested byte: no bytes before it
		// are fetched for a consumer that seeked into the middle of a block.
		block = r.fetchLocked(index, offset, end)
	}
	r.tick++
	block.used = r.tick
	if sequential {
		last := index + int64(r.config.PrefetchBlocks)
		for next := index + 1; next <= last; next++ {
			start := next * blockBytes
			if start >= size || (offset < r.hint && start >= r.hint) {
				break
			}
			if r.blocks[next] != nil {
				continue
			}
			if !r.makeRoomLocked(index, last) {
				break
			}
			end := min(start+blockBytes, size)
			if offset < r.hint {
				end = min(end, r.hint)
			}
			r.fetchLocked(next, start, end)
		}
	}
	return block, nil
}

// makeRoomLocked evicts least recently used finished blocks outside
// [keepFirst, keepLast]. Prefetch stops when no slot can be freed; a demand
// fetch always proceeds, so the cache may briefly exceed CacheBlocks by the
// number of concurrent readers.
func (r *ReadAhead) makeRoomLocked(keepFirst, keepLast int64) bool {
	for len(r.blocks) >= r.config.CacheBlocks {
		victim, oldest := int64(-1), uint64(0)
		for index, block := range r.blocks {
			if block.ready && (index < keepFirst || index > keepLast) && (victim < 0 || block.used < oldest) {
				victim, oldest = index, block.used
			}
		}
		if victim < 0 {
			return false
		}
		delete(r.blocks, victim)
	}
	return true
}

func (r *ReadAhead) fetchLocked(index, start, end int64) *readAheadBlock {
	r.tick++
	block := &readAheadBlock{start: start, end: end, used: r.tick, done: make(chan struct{})}
	r.blocks[index] = block
	r.inflight++
	r.fetches.Add(1)
	go func() {
		defer r.fetches.Done()
		data := make([]byte, end-start)
		n, err := r.source.ReadAt(data, start)
		if n != len(data) && err == nil {
			err = io.ErrUnexpectedEOF
		} else if n == len(data) && errors.Is(err, io.EOF) && end == r.info.Size {
			err = nil
		}
		r.mu.Lock()
		if err != nil {
			clear(data)
			block.err = err
			if r.blocks[index] == block {
				delete(r.blocks, index)
			}
		} else {
			block.data = data
		}
		block.ready = true
		r.inflight--
		r.mu.Unlock()
		close(block.done)
	}()
	return block
}

func (r *ReadAhead) Read(p []byte) (int, error) {
	r.cursorMu.Lock()
	defer r.cursorMu.Unlock()
	n, err := r.ReadAt(p, r.cursor)
	r.cursor += int64(n)
	return n, err
}

func (r *ReadAhead) Seek(offset int64, whence int) (int64, error) {
	r.cursorMu.Lock()
	defer r.cursorMu.Unlock()
	if err := r.admit(); err != nil {
		return 0, err
	}
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = r.cursor
	case io.SeekEnd:
		base = r.info.Size
	default:
		return 0, errors.New("invalid native seek origin")
	}
	if (offset > 0 && base > 1<<63-1-offset) || (offset < 0 && offset < -base) {
		return 0, errors.New("invalid native seek offset")
	}
	r.cursor = base + offset
	return r.cursor, nil
}

// Close drops the cache, cancels fetches still in flight, joins them and then
// closes the source exactly once. Close the reader only after checking the
// consumer's result: canceling an in-flight fetch fails the source.
func (r *ReadAhead) Close() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		inflight := r.inflight > 0
		r.blocks = map[int64]*readAheadBlock{}
		r.mu.Unlock()
		close(r.closing)
		if inflight {
			r.source.Cancel()
		}
		r.fetches.Wait()
		r.closeErr = r.source.Close()
	})
	return r.closeErr
}

var _ mediasource.File = (*ReadAhead)(nil)
