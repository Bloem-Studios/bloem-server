package nativestorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
)

var (
	errReadAheadRevoked = errors.New("fixture authority revoked")
	errReadAheadStopped = errors.New("fixture session stopped")
)

// readAheadFake is an admitted provider file with injected per-read and
// per-check latency. Cancel interrupts reads in flight, as PlaybackFile does.
type readAheadFake struct {
	data        []byte
	latency     time.Duration
	authLatency time.Duration
	failAt      int64

	reads, bytesRead, reauths, closes atomic.Int64
	active, maxActive, maxEnd         atomic.Int64
	revoked, stopped                  atomic.Bool
	cancelOnce                        sync.Once
	cancelled                         chan struct{}
}

func newReadAheadFake(size int) *readAheadFake {
	data := make([]byte, size)
	random := rand.New(rand.NewPCG(7, uint64(size)))
	for i := range data {
		data[i] = byte(random.Uint32())
	}
	return &readAheadFake{data: data, failAt: -1, cancelled: make(chan struct{})}
}

func (f *readAheadFake) Info() mediasource.Info {
	return mediasource.Info{Name: "movie.mkv", Revision: "etag-1", Size: int64(len(f.data))}
}
func (f *readAheadFake) Read([]byte) (int, error) { return 0, errors.New("read-ahead must use ReadAt") }
func (f *readAheadFake) Seek(int64, int) (int64, error) {
	return 0, errors.New("read-ahead must not seek its source")
}
func (f *readAheadFake) Close() error { f.closes.Add(1); return nil }
func (f *readAheadFake) Cancel()      { f.cancelOnce.Do(func() { close(f.cancelled) }) }
func (f *readAheadFake) Reauthorize(ctx context.Context) error {
	f.reauths.Add(1)
	if f.authLatency > 0 {
		time.Sleep(f.authLatency)
	}
	if err := f.Err(); err != nil {
		return err
	}
	if f.revoked.Load() {
		return errReadAheadRevoked
	}
	return ctx.Err()
}
func (f *readAheadFake) Err() error {
	select {
	case <-f.cancelled:
		return context.Canceled
	default:
	}
	if f.stopped.Load() {
		return errReadAheadStopped
	}
	return nil
}
func (f *readAheadFake) ReadAt(p []byte, offset int64) (int, error) {
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		peak := f.maxActive.Load()
		if active <= peak || f.maxActive.CompareAndSwap(peak, active) {
			break
		}
	}
	f.reads.Add(1)
	if f.latency > 0 {
		select {
		case <-time.After(f.latency):
		case <-f.cancelled:
			clear(p)
			return 0, context.Canceled
		}
	}
	if err := f.Err(); err != nil {
		clear(p)
		return 0, err
	}
	// Like the PlaybackFile guard, a revoked source refuses provider reads.
	if f.revoked.Load() {
		clear(p)
		return 0, errReadAheadRevoked
	}
	end := offset + int64(len(p))
	if f.failAt >= offset && f.failAt < end {
		clear(p)
		return 0, errors.New("fixture provider failure")
	}
	for {
		peak := f.maxEnd.Load()
		if end <= peak || f.maxEnd.CompareAndSwap(peak, end) {
			break
		}
	}
	if offset >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[offset:])
	f.bytesRead.Add(int64(n))
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func newTestReadAhead(t *testing.T, fake *readAheadFake, config ReadAheadConfig) *ReadAhead {
	t.Helper()
	reader, err := NewReadAhead(t.Context(), fake, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

var smallReadAhead = ReadAheadConfig{BlockBytes: 64 << 10, CacheBlocks: 6, PrefetchBlocks: 3, AuthorizationWindow: time.Second}

func TestReadAheadConfigBounds(t *testing.T) {
	fake := newReadAheadFake(10)
	for name, config := range map[string]ReadAheadConfig{
		"tiny block":     {BlockBytes: 4 << 10, CacheBlocks: 4, PrefetchBlocks: 1, AuthorizationWindow: time.Second},
		"huge block":     {BlockBytes: 16 << 20, CacheBlocks: 2, PrefetchBlocks: 1, AuthorizationWindow: time.Second},
		"cache too big":  {BlockBytes: 8 << 20, CacheBlocks: 9, PrefetchBlocks: 1, AuthorizationWindow: time.Second},
		"no room":        {BlockBytes: 1 << 20, CacheBlocks: 3, PrefetchBlocks: 3, AuthorizationWindow: time.Second},
		"no window":      {BlockBytes: 1 << 20, CacheBlocks: 4, PrefetchBlocks: 1},
		"endless window": {BlockBytes: 1 << 20, CacheBlocks: 4, PrefetchBlocks: 1, AuthorizationWindow: time.Hour},
	} {
		if _, err := NewReadAhead(t.Context(), fake, config); !errors.Is(err, ErrReadAheadInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReadAheadMatchesSourceBytes(t *testing.T) {
	fake := newReadAheadFake(5<<20 + 12345)
	size := int64(len(fake.data))
	t.Run("sequential", func(t *testing.T) {
		reader := newTestReadAhead(t, fake, smallReadAhead)
		digest := sha256.New()
		buffer := make([]byte, 32<<10)
		n, err := io.CopyBuffer(digest, struct{ io.Reader }{reader}, buffer)
		if err != nil || n != size {
			t.Fatalf("copied %d of %d: %v", n, size, err)
		}
		if want := sha256.Sum256(fake.data); !bytes.Equal(digest.Sum(nil), want[:]) {
			t.Fatal("sequential bytes differ from the source")
		}
	})
	t.Run("positional", func(t *testing.T) {
		reader := newTestReadAhead(t, fake, smallReadAhead)
		random := rand.New(rand.NewPCG(1, 2))
		cases := [][2]int64{{0, 1}, {65535, 2}, {65536, 65536}, {100, 300 << 10}, {size - 10, 10}, {size - 1, 1}}
		for range 200 {
			offset := random.Int64N(size)
			cases = append(cases, [2]int64{offset, 1 + random.Int64N(min(size-offset, 400<<10))})
		}
		for _, c := range cases {
			buffer := make([]byte, c[1])
			n, err := reader.ReadAt(buffer, c[0])
			if err != nil || int64(n) != c[1] || !bytes.Equal(buffer, fake.data[c[0]:c[0]+c[1]]) {
				t.Fatalf("ReadAt(%d, %d) = %d, %v", c[0], c[1], n, err)
			}
		}
		buffer := make([]byte, 20)
		if n, err := reader.ReadAt(buffer, size-5); n != 5 || !errors.Is(err, io.EOF) || !bytes.Equal(buffer[:5], fake.data[size-5:]) {
			t.Fatalf("short tail read = %d, %v", n, err)
		}
		if n, err := reader.ReadAt(buffer, size); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("read at end = %d, %v", n, err)
		}
	})
	t.Run("seek", func(t *testing.T) {
		reader := newTestReadAhead(t, fake, smallReadAhead)
		if end, err := reader.Seek(0, io.SeekEnd); err != nil || end != size {
			t.Fatalf("seek end = %d, %v", end, err)
		}
		for _, offset := range []int64{size / 2, 3, size - 70000, 1 << 20} {
			if _, err := reader.Seek(offset, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, 70000)
			n, err := io.ReadFull(reader, buffer)
			if want := min(int64(len(buffer)), size-offset); int64(n) != want || (err != nil && want == int64(len(buffer))) || !bytes.Equal(buffer[:n], fake.data[offset:offset+want]) {
				t.Fatalf("seek %d read %d: %v", offset, n, err)
			}
		}
	})
	t.Run("range hint", func(t *testing.T) {
		hinted := newReadAheadFake(len(fake.data))
		hinted.data = fake.data
		reader := newTestReadAhead(t, hinted, smallReadAhead)
		start, end := int64(200_000), int64(300_000)
		reader.SetRangeEnd(end)
		buffer := make([]byte, end-start)
		if n, err := reader.ReadAt(buffer, start); err != nil || int64(n) != end-start || !bytes.Equal(buffer, fake.data[start:end]) {
			t.Fatalf("hinted range = %d, %v", n, err)
		}
		if got := hinted.maxEnd.Load(); got > end {
			t.Fatalf("fetched to %d past the range end %d", got, end)
		}
		if got := hinted.bytesRead.Load(); got != end-start {
			t.Fatalf("fetched %d bytes for a %d byte range", got, end-start)
		}
		// The hint never limits correctness.
		tail := make([]byte, 100_000)
		if n, err := reader.ReadAt(tail, end); err != nil || n != len(tail) || !bytes.Equal(tail, fake.data[end:end+int64(len(tail))]) {
			t.Fatalf("read past the hint = %d, %v", n, err)
		}
	})
}

// http.ServeContent remains the Range authority; the reader must make every
// form, including suffix and multipart ranges, byte-identical to the source.
func TestReadAheadServeContentRanges(t *testing.T) {
	fake := newReadAheadFake(3<<20 + 77)
	size := int64(len(fake.data))
	for _, header := range []string{"", "bytes=0-1", "bytes=0-0", "bytes=1000-", "bytes=-500", "bytes=65530-65545", "bytes=1000-2200000",
		fmt.Sprintf("bytes=%d-%d", size-1, size+100), "bytes=0-9,131072-131200", fmt.Sprintf("bytes=%d-", size), "bytes=5-2"} {
		t.Run(header, func(t *testing.T) {
			serve := func(content io.ReadSeeker, hint func(int64)) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, "/movie", nil)
				if header != "" {
					r.Header.Set("Range", header)
				}
				if hint != nil {
					hint(RangeEnd(header, size))
				}
				w := httptest.NewRecorder()
				w.Header().Set("Content-Type", "video/x-matroska")
				http.ServeContent(w, r, "movie.mkv", time.Unix(1_760_000_000, 0), content)
				return w
			}
			want := serve(bytes.NewReader(fake.data), nil)
			reader := newTestReadAhead(t, fake, smallReadAhead)
			got := serve(reader, reader.SetRangeEnd)
			if got.Code != want.Code || got.Header().Get("Content-Range") != want.Header().Get("Content-Range") || got.Header().Get("Content-Length") != want.Header().Get("Content-Length") {
				t.Fatalf("status %d %v, want %d %v", got.Code, got.Header(), want.Code, want.Header())
			}
			gotBody, wantBody := got.Body.Bytes(), want.Body.Bytes()
			gotType, wantType := got.Header().Get("Content-Type"), want.Header().Get("Content-Type")
			if strings.HasPrefix(wantType, "multipart/") {
				// Multipart boundaries are random; compare the parts' bytes instead.
				gotBody, wantBody = bytes.ReplaceAll(gotBody, boundary(gotType), nil), bytes.ReplaceAll(wantBody, boundary(wantType), nil)
				gotType, wantType = strings.ReplaceAll(gotType, string(boundary(gotType)), ""), strings.ReplaceAll(wantType, string(boundary(wantType)), "")
			}
			if gotType != wantType {
				t.Fatalf("content type %q, want %q", gotType, wantType)
			}
			if !bytes.Equal(gotBody, wantBody) {
				t.Fatalf("body differs: %d bytes, want %d", len(gotBody), len(wantBody))
			}
		})
	}
}

func boundary(contentType string) []byte {
	_, value, _ := strings.Cut(contentType, "boundary=")
	return []byte(value)
}

func TestRangeEnd(t *testing.T) {
	for header, want := range map[string]int64{"": 100, "bytes=0-9": 10, "bytes=10-": 100, "bytes=-5": 100, "bytes=0-9,20-29": 100,
		"bytes=5-200": 100, "bytes=9-5": 100, "items=0-9": 100, "bytes=a-9": 100, "bytes= 3 - 4 ": 5, "bytes=99-99": 100} {
		if got := RangeEnd(header, 100); got != want {
			t.Errorf("RangeEnd(%q) = %d, want %d", header, got, want)
		}
	}
}

// Cached bytes keep flowing only inside the authorization window after a
// revocation that only Reauthorize can see; a stopped source stops at once.
func TestReadAheadRevocationStopsDeliveryWithinWindow(t *testing.T) {
	const window = 100 * time.Millisecond
	fake := newReadAheadFake(512 << 10)
	reader := newTestReadAhead(t, fake, ReadAheadConfig{BlockBytes: 64 << 10, CacheBlocks: 8, PrefetchBlocks: 7, AuthorizationWindow: window})
	buffer := make([]byte, 1024)
	if _, err := reader.ReadAt(buffer, 0); err != nil {
		t.Fatal(err)
	}
	waitReadAhead(t, func() bool { return fake.bytesRead.Load() == int64(len(fake.data)) })
	reads := fake.reads.Load()
	revoked := time.Now()
	fake.revoked.Store(true)
	var last time.Time
	served := 0
	for offset := int64(0); ; offset = (offset + 4096) % int64(len(fake.data)-4096) {
		if time.Since(revoked) > 5*window {
			t.Fatal("cached bytes were served long after revocation")
		}
		if _, err := reader.ReadAt(buffer, offset); err != nil {
			if !errors.Is(err, errReadAheadRevoked) {
				t.Fatalf("revocation error = %v", err)
			}
			break
		}
		last = time.Now()
		served++
	}
	if late := last.Sub(revoked); late > window+50*time.Millisecond {
		t.Fatalf("delivery continued %s after revocation, window %s", late, window)
	}
	if fake.reads.Load() != reads {
		t.Fatal("revoked reader fetched from the provider")
	}
	t.Logf("served %d cached reads for %s after revocation (window %s)", served, last.Sub(revoked), window)
	if n, err := reader.ReadAt(buffer, 0); n != 0 || !errors.Is(err, errReadAheadRevoked) || !bytes.Equal(buffer, make([]byte, len(buffer))) {
		t.Fatalf("revocation was not sticky: %d %v", n, err)
	}

	stopped := newReadAheadFake(256 << 10)
	reader = newTestReadAhead(t, stopped, ReadAheadConfig{BlockBytes: 64 << 10, CacheBlocks: 4, PrefetchBlocks: 3, AuthorizationWindow: 10 * time.Second})
	if _, err := reader.ReadAt(buffer, 0); err != nil {
		t.Fatal(err)
	}
	waitReadAhead(t, func() bool { return stopped.bytesRead.Load() == int64(len(stopped.data)) })
	stopped.stopped.Store(true)
	if n, err := reader.ReadAt(buffer, 70_000); n != 0 || !errors.Is(err, errReadAheadStopped) {
		t.Fatalf("stopped source served cached bytes: %d %v", n, err)
	}
}

func TestReadAheadExplicitReauthorizeOpensWindow(t *testing.T) {
	fake := newReadAheadFake(256 << 10)
	reader := newTestReadAhead(t, fake, ReadAheadConfig{BlockBytes: 64 << 10, CacheBlocks: 4, PrefetchBlocks: 3, AuthorizationWindow: 10 * time.Second})
	if err := reader.Reauthorize(t.Context()); err != nil || fake.reauths.Load() != 1 {
		t.Fatalf("reauthorize %v checks %d", err, fake.reauths.Load())
	}
	if _, err := io.Copy(io.Discard, struct{ io.Reader }{reader}); err != nil || fake.reauths.Load() != 1 {
		t.Fatalf("reads inside the window rechecked: %v checks %d", err, fake.reauths.Load())
	}
	fake.revoked.Store(true)
	if err := reader.Reauthorize(t.Context()); !errors.Is(err, errReadAheadRevoked) || !errors.Is(reader.Err(), errReadAheadRevoked) {
		t.Fatalf("explicit check after revocation = %v", err)
	}
}

func TestReadAheadProviderFailureIsSticky(t *testing.T) {
	fake := newReadAheadFake(1 << 20)
	fake.failAt = 300_000
	reader := newTestReadAhead(t, fake, ReadAheadConfig{BlockBytes: 64 << 10, CacheBlocks: 2, PrefetchBlocks: 0, AuthorizationWindow: time.Second})
	buffer := bytes.Repeat([]byte{0xAA}, 200_000)
	if n, err := reader.ReadAt(buffer, 200_000); n != 0 || err == nil || !bytes.Equal(buffer, make([]byte, len(buffer))) {
		t.Fatalf("failed read committed bytes: %d %v", n, err)
	}
	if _, err := reader.ReadAt(buffer[:10], 0); err == nil || !errors.Is(reader.Err(), err) {
		t.Fatalf("failure was not sticky: %v", err)
	}
}

func TestReadAheadConcurrentReaders(t *testing.T) {
	fake := newReadAheadFake(4 << 20)
	fake.latency = time.Millisecond
	config := ReadAheadConfig{BlockBytes: 64 << 10, CacheBlocks: 6, PrefetchBlocks: 3, AuthorizationWindow: 5 * time.Millisecond}
	reader := newTestReadAhead(t, fake, config)
	const readers = 8
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for worker := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			random := rand.New(rand.NewPCG(uint64(worker), 99))
			offset := random.Int64N(int64(len(fake.data)))
			for range 150 {
				if random.IntN(4) == 0 {
					offset = random.Int64N(int64(len(fake.data)))
				}
				length := min(1+random.Int64N(96<<10), int64(len(fake.data))-offset)
				buffer := make([]byte, length)
				n, err := reader.ReadAt(buffer, offset)
				if err != nil || int64(n) != length || !bytes.Equal(buffer, fake.data[offset:offset+length]) {
					errs <- fmt.Errorf("worker %d ReadAt(%d, %d) = %d: %w", worker, offset, length, n, err)
					return
				}
				offset = (offset + length) % int64(len(fake.data))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	// Fetches are bounded by the cache plus, per reader, one demand fetch and
	// one superseded fetch still finishing.
	if peak := fake.maxActive.Load(); peak > int64(config.CacheBlocks+2*readers) {
		t.Fatalf("%d concurrent provider reads", peak)
	}
	if err := reader.Close(); err != nil || fake.closes.Load() != 1 || fake.active.Load() != 0 {
		t.Fatalf("close %v closes %d active %d", err, fake.closes.Load(), fake.active.Load())
	}
}

func TestReadAheadCloseCancelsAndJoinsFetches(t *testing.T) {
	fake := newReadAheadFake(1 << 20)
	fake.latency = time.Hour
	reader, err := NewReadAhead(t.Context(), fake, smallReadAhead)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := reader.ReadAt(make([]byte, 10), 0)
		result <- err
	}()
	waitReadAhead(t, func() bool { return fake.active.Load() > 0 })
	closed := make(chan error, 1)
	go func() { closed <- reader.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the in-flight fetch")
	}
	if err := <-result; err == nil {
		t.Fatal("read survived Close")
	}
	if fake.active.Load() != 0 || fake.closes.Load() != 1 {
		t.Fatalf("active %d closes %d", fake.active.Load(), fake.closes.Load())
	}
	if _, err := reader.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("read after Close = %v", err)
	}
	if err := reader.Close(); err != nil || fake.closes.Load() != 1 {
		t.Fatal("Close is not idempotent")
	}
}

// Idle Close must not cancel the source: a completed response is checked
// first, and canceling would fail it.
func TestReadAheadIdleCloseDoesNotCancel(t *testing.T) {
	fake := newReadAheadFake(64 << 10)
	reader, err := NewReadAhead(t.Context(), fake, smallReadAhead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, struct{ io.Reader }{reader}); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil || fake.Err() != nil {
		t.Fatalf("idle close %v source %v", err, fake.Err())
	}
}

func waitReadAhead(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

// Live S3 direct play reached about 5 Mbit/s: each 32 KiB copy cost a provider
// read of about 50 ms. The playback configuration must reach 40 Mbit/s.
var playbackReadAhead = ReadAheadConfig{BlockBytes: 1 << 20, CacheBlocks: 5, PrefetchBlocks: 3, AuthorizationWindow: time.Second}

func readAheadThroughput(tb testing.TB, size int, config ReadAheadConfig) float64 {
	fake := newReadAheadFake(size)
	fake.latency, fake.authLatency = 50*time.Millisecond, 5*time.Millisecond
	reader, err := NewReadAhead(context.Background(), fake, config)
	if err != nil {
		tb.Fatal(err)
	}
	defer reader.Close()
	started := time.Now()
	digest := sha256.New()
	n, err := io.CopyBuffer(digest, struct{ io.Reader }{reader}, make([]byte, 32<<10))
	elapsed := time.Since(started)
	if err != nil || n != int64(size) {
		tb.Fatalf("copied %d: %v", n, err)
	}
	if want := sha256.Sum256(fake.data); !bytes.Equal(digest.Sum(nil), want[:]) {
		tb.Fatal("bytes differ from the source")
	}
	return float64(n*8) / elapsed.Seconds() / 1e6
}

func TestReadAheadThroughputWithProviderLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput check")
	}
	mbps := readAheadThroughput(t, 16<<20, playbackReadAhead)
	t.Logf("read-ahead: %.1f Mbit/s at 50 ms per provider read (32 KiB reads alone: %.1f Mbit/s)", mbps, float64(32<<10*8)/0.05/1e6)
	if mbps < 40 {
		t.Fatalf("read-ahead reached %.1f Mbit/s, want at least 40", mbps)
	}
}

func BenchmarkReadAheadProviderLatency(b *testing.B) {
	for range b.N {
		b.ReportMetric(readAheadThroughput(b, 16<<20, playbackReadAhead), "Mbit/s")
	}
}
