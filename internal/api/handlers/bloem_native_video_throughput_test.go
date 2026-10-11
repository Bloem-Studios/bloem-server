package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// nativeVideoLatencyFile charges every provider read a fixed round trip, as a
// checked-ETag S3 read (HEAD + ranged GET + HEAD) plus reauthorization does.
type nativeVideoLatencyFile struct {
	NativeVideoFile
	latency time.Duration
	reads   *atomic.Int64
}

func (f *nativeVideoLatencyFile) Read(p []byte) (int, error) {
	f.reads.Add(1)
	time.Sleep(f.latency)
	return f.NativeVideoFile.Read(p)
}
func (f *nativeVideoLatencyFile) ReadAt(p []byte, offset int64) (int, error) {
	f.reads.Add(1)
	time.Sleep(f.latency)
	return f.NativeVideoFile.ReadAt(p, offset)
}

// The live build delivered about 5 Mbit/s per direct-play stream: each 32 KiB
// copy of http.ServeContent was one provider read. A 1080p remux source needs
// 30-40 Mbit/s, so a single stream must reach 40 Mbit/s at 50 ms per read.
func TestNativeVideoDirectPlayThroughputWithProviderLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput check")
	}
	const size = 8 << 20
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 31)
	}
	s, file, session, identity, _ := nativeVideoFixture(t)
	reads := new(atomic.Int64)
	closed := new(int)
	s.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		inner := &videoFileFixture{Reader: bytes.NewReader(data), id: *identity, info: mediasource.Info{Name: "movie.mp4", Size: size, Revision: identity.Reference.Revision}, closed: closed}
		return &nativeVideoLatencyFile{NativeVideoFile: inner, latency: 50 * time.Millisecond, reads: reads}, nil
	}
	if err := s.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.Serve(w, r, session, file); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	request.Header.Set("Range", "bytes=1000-")
	started := time.Now()
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	digest := sha256.New()
	n, err := digest.Write(mustReadAll(t, response))
	elapsed := time.Since(started)
	if err != nil || response.StatusCode != http.StatusPartialContent || n != size-1000 {
		t.Fatalf("status %d bytes %d err %v", response.StatusCode, n, err)
	}
	if want := sha256.Sum256(data[1000:]); !bytes.Equal(digest.Sum(nil), want[:]) {
		t.Fatal("direct-play bytes differ from the source")
	}
	mbps := float64(n*8) / elapsed.Seconds() / 1e6
	t.Logf("direct play: %d bytes in %s = %.1f Mbit/s with %d provider reads", n, elapsed, mbps, reads.Load())
	if mbps < 40 {
		t.Fatalf("direct play reached %.1f Mbit/s, want at least 40", mbps)
	}
}

func mustReadAll(t *testing.T, response *http.Response) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if _, err := buffer.ReadFrom(response.Body); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// Progressive remux and HLS feed FFmpeg through the tool bridge. The live
// build's 128 KiB chunks with one provider read each gave about 20 Mbit/s; a
// 1080p remux source needs 30-40 Mbit/s in real time.
func TestNativeVideoToolInputThroughputWithProviderLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput check")
	}
	const size = 12 << 20
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 17)
	}
	s, file, session, identity, _ := nativeVideoFixture(t)
	reads := new(atomic.Int64)
	closed := new(int)
	s.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		inner := &videoFileFixture{Reader: bytes.NewReader(data), id: *identity, info: mediasource.Info{Name: "movie.mkv", Size: size, Revision: identity.Reference.Revision}, closed: closed}
		return &nativeVideoLatencyFile{NativeVideoFile: inner, latency: 50 * time.Millisecond, reads: reads}, nil
	}
	if err := s.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	source, format, err := s.InputSource(t.Context(), session, file)
	if err != nil {
		t.Fatal(err)
	}
	input, err := source.Open(t.Context(), playback.TranscodeOpts{InputPath: file.FilePath, SessionID: session.ID, InputFormat: format})
	if err != nil {
		t.Fatal(err)
	}
	var mbps float64
	err = input.Run(func(ctx context.Context, argument string) error {
		get := func(header string) (*http.Response, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, argument, nil)
			if err != nil {
				return nil, err
			}
			request.Header.Set("Range", header)
			return http.DefaultClient.Do(request)
		}
		// FFmpeg probes the head, reconnects for the index at the tail while
		// the first range is still open, then reads the body sequentially.
		head, err := get("bytes=0-")
		if err != nil {
			return err
		}
		if _, err := head.Body.Read(make([]byte, 4096)); err != nil {
			return err
		}
		tail, err := get(fmt.Sprintf("bytes=%d-", size-200_000))
		if err != nil {
			return err
		}
		tailBytes, err := io.ReadAll(tail.Body)
		tail.Body.Close()
		head.Body.Close()
		if err != nil || !bytes.Equal(tailBytes, data[size-200_000:]) {
			return fmt.Errorf("tail range: %d bytes: %w", len(tailBytes), err)
		}
		started := time.Now()
		body, err := get("bytes=0-")
		if err != nil {
			return err
		}
		defer body.Body.Close()
		digest := sha256.New()
		n, err := io.Copy(digest, body.Body)
		elapsed := time.Since(started)
		if want := sha256.Sum256(data); err != nil || n != size || !bytes.Equal(digest.Sum(nil), want[:]) {
			return fmt.Errorf("tool input body: %d bytes: %w", n, err)
		}
		mbps = float64(n*8) / elapsed.Seconds() / 1e6
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("tool input: %.1f Mbit/s with %d provider reads", mbps, reads.Load())
	if mbps < 40 {
		t.Fatalf("tool input reached %.1f Mbit/s, want at least 40", mbps)
	}
}
