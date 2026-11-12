package remotesource

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func blob(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i*7 + i/251)
	}
	return data
}

type mediaServer struct {
	data    atomic.Pointer[[]byte]
	etag    atomic.Pointer[string]
	noRange atomic.Bool
	ranges  atomic.Int32
}

func (m *mediaServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data := *m.data.Load()
	if etag := m.etag.Load(); etag != nil {
		w.Header().Set("ETag", *etag)
	}
	if m.noRange.Load() {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
		return
	}
	if r.Header.Get("Range") != "" {
		m.ranges.Add(1)
	}
	http.ServeContent(w, r, "movie.mkv", time.Unix(1_700_000_000, 0), bytes.NewReader(data))
}

func openTest(t *testing.T, server *mediaServer) (*File, *Guard, string) {
	t.Helper()
	httpServer := serverOn(t, "127.0.0.1", server)
	g := testGuard(&fakeResolver{answers: map[string][][]netip.Addr{"cdn.test": {addrs("127.0.0.1")}}}, nil)
	raw := "http://cdn.test:" + port(t, httpServer) + "/films/movie.mkv"
	f, err := Open(context.Background(), g.Client(), raw, NameHint(raw))
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f, g, raw
}

func TestFileReadsRangesAcrossBlocks(t *testing.T) {
	data := blob(3*BlockBytes + 12345)
	server := &mediaServer{}
	server.data.Store(&data)
	f, _, raw := openTest(t, server)
	info := f.Info()
	if info.Size != int64(len(data)) || info.Revision != Revision(raw) || info.Name != "movie.mkv" {
		t.Fatalf("Info = %+v", info)
	}
	for _, c := range []struct{ off, n int }{{0, 10}, {BlockBytes - 5, 10}, {2*BlockBytes + 3, BlockBytes}, {len(data) - 100, 100}, {12345, 3 * BlockBytes}} {
		got := make([]byte, c.n)
		n, err := f.ReadAt(got, int64(c.off))
		want := data[c.off:min(c.off+c.n, len(data))]
		if n != len(want) || !bytes.Equal(got[:n], want) {
			t.Fatalf("ReadAt(%d,%d) = %d, %v; bytes differ", c.off, c.n, n, err)
		}
		if c.off+c.n > len(data) && !errors.Is(err, io.EOF) {
			t.Fatalf("ReadAt past the end = %v, want EOF", err)
		}
	}
	if n, err := f.ReadAt(make([]byte, 4), int64(len(data))); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt at size = %d, %v", n, err)
	}
	if _, err := f.Seek(int64(len(data)-3), io.SeekStart); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(all, data[len(data)-3:]) {
		t.Fatalf("Read tail = %v, %v", all, err)
	}
}

func TestFileSmallBodyWithoutRanges(t *testing.T) {
	data := blob(5000)
	server := &mediaServer{}
	server.data.Store(&data)
	server.noRange.Store(true)
	f, _, _ := openTest(t, server)
	got := make([]byte, 100)
	if n, err := f.ReadAt(got, 4000); n != 100 || err != nil || !bytes.Equal(got, data[4000:4100]) {
		t.Fatalf("ReadAt = %d, %v", n, err)
	}
}

func TestFileLargeBodyWithoutRangesIsRefused(t *testing.T) {
	data := blob(BlockBytes + 1)
	server := &mediaServer{}
	server.data.Store(&data)
	server.noRange.Store(true)
	httpServer := serverOn(t, "127.0.0.1", server)
	g := testGuard(&fakeResolver{}, nil)
	if _, err := Open(context.Background(), g.Client(), "http://127.0.0.1:"+port(t, httpServer)+"/x.mkv", "x.mkv"); !errors.Is(err, ErrRangeUnsupported) {
		t.Fatalf("Open = %v, want ErrRangeUnsupported", err)
	}
}

func TestFileFailsWhenTheRemoteChanges(t *testing.T) {
	data := blob(3 * BlockBytes)
	etag := `"one"`
	server := &mediaServer{}
	server.data.Store(&data)
	server.etag.Store(&etag)
	f, _, _ := openTest(t, server)
	changed := `"two"`
	server.etag.Store(&changed)
	if _, err := f.ReadAt(make([]byte, 10), 2*BlockBytes); !errors.Is(err, ErrChanged) {
		t.Fatalf("ReadAt after an ETag change = %v, want ErrChanged", err)
	}
	if !errors.Is(f.Err(), ErrChanged) {
		t.Fatalf("Err = %v, want sticky ErrChanged", f.Err())
	}

	server2 := &mediaServer{}
	server2.data.Store(&data)
	g, _, _ := openTest(t, server2)
	shorter := blob(2 * BlockBytes)
	server2.data.Store(&shorter)
	if _, err := g.ReadAt(make([]byte, 10), BlockBytes+5); !errors.Is(err, ErrChanged) {
		t.Fatalf("ReadAt after a size change = %v, want ErrChanged", err)
	}
}

func TestFileErrorsNeverQuoteTheURL(t *testing.T) {
	g := NewGuard(Options{Resolver: &fakeResolver{}})
	secret := "http://10.1.2.3/movie.mkv?token=hunter2"
	_, err := Open(context.Background(), g.Client(), secret, "movie.mkv")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("Open = %v, want ErrBlockedAddress", err)
	}
	if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "10.1.2.3") {
		t.Fatalf("error quotes the URL: %v", err)
	}
	notFound := serverOn(t, "127.0.0.1", http.NotFoundHandler())
	pg := testGuard(&fakeResolver{}, nil)
	_, err = Open(context.Background(), pg.Client(), "http://127.0.0.1:"+port(t, notFound)+"/m.mkv?token=hunter2", "m.mkv")
	var status *HTTPStatusError
	if !errors.As(err, &status) || status.Status != http.StatusNotFound || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("Open of a missing file = %v", err)
	}
}

func TestFileCloseCancelsReads(t *testing.T) {
	data := blob(2 * BlockBytes)
	server := &mediaServer{}
	server.data.Store(&data)
	f, _, _ := openTest(t, server)
	_ = f.Close()
	if _, err := f.ReadAt(make([]byte, 10), BlockBytes+1); err == nil {
		t.Fatal("ReadAt after Close must fail")
	}
}
