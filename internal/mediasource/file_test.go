package mediasource

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

var bookRef = Ref{SourceID: "fixture", EntryID: "book", Revision: "v1"}

// Omitting revision pinning or emitting the wrong byte range must fail here.
type rangeSource struct {
	data   []byte
	mu     sync.Mutex
	ranges [][2]int64
	stats  int
}

func (s *rangeSource) Stat(ctx context.Context, ref Ref) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	s.stats++
	s.mu.Unlock()
	return Info{Name: "book.epub", Revision: "v1", Size: int64(len(s.data))}, nil
}
func (s *rangeSource) ReadRange(ctx context.Context, ref Ref, offset, length int64, w io.Writer) error {
	if ref.Revision != "v1" {
		return errors.New("unpinned read")
	}
	if length > 8<<20 || offset < 0 || length < 0 || offset > int64(len(s.data))-length {
		return errors.New("invalid range")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.ranges = append(s.ranges, [2]int64{offset, length})
	s.mu.Unlock()
	_, err := w.Write(s.data[offset : offset+length])
	return err
}

func TestSeekableFileAndEOF(t *testing.T) {
	source := &rangeSource{data: []byte("0123456789")}
	f, err := Open(context.Background(), source, bookRef)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, tc := range []struct {
		offset int64
		whence int
		want   int64
		text   string
	}{
		{2, io.SeekStart, 2, "23"}, {2, io.SeekCurrent, 6, "67"}, {-2, io.SeekEnd, 8, "89"},
	} {
		pos, err := f.Seek(tc.offset, tc.whence)
		if err != nil || pos != tc.want {
			t.Fatalf("seek: %d, %v", pos, err)
		}
		b := make([]byte, 2)
		n, err := f.Read(b)
		if err != nil || n != 2 || string(b) != tc.text {
			t.Fatalf("read: %q %d %v", b, n, err)
		}
	}
	if _, err := f.Seek(-1, io.SeekStart); err == nil {
		t.Fatal("negative seek accepted")
	}
	if _, err := f.Seek(0, 99); err == nil {
		t.Fatal("bad whence accepted")
	}
	if _, err := f.Seek(1<<63-1, io.SeekEnd); err == nil {
		t.Fatal("overflow seek accepted")
	}
	for _, tc := range []struct {
		offset        int64
		length, wantN int
		wantErr       error
		want          string
	}{
		{8, 4, 2, io.EOF, "89"}, {10, 2, 0, io.EOF, ""}, {12, 0, 0, nil, ""}, {0, 0, 0, nil, ""},
	} {
		b := make([]byte, tc.length)
		n, err := f.ReadAt(b, tc.offset)
		if n != tc.wantN || !errors.Is(err, tc.wantErr) || string(b[:n]) != tc.want {
			t.Fatalf("offset %d: %d %q %v", tc.offset, n, b[:n], err)
		}
	}
	if _, err := f.ReadAt(make([]byte, 1), -1); err == nil {
		t.Fatal("negative ReadAt accepted")
	}
	if source.stats != 1 {
		t.Fatalf("Stat repeated: %d", source.stats)
	}
}

func TestLargeAndConcurrentReaderAt(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<20)
	source := &rangeSource{data: data}
	f, err := Open(context.Background(), source, bookRef)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := make([]byte, len(data))
	n, err := f.ReadAt(b, 0)
	if err != nil || n != len(b) || !bytes.Equal(b, data) {
		t.Fatalf("large read: %d %v", n, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(offset int64) {
			defer wg.Done()
			b := make([]byte, 4)
			n, err := f.ReadAt(b, offset)
			if err != nil || n != 4 || !bytes.Equal(b, data[offset:offset+4]) {
				t.Errorf("concurrent read: %d %v", n, err)
			}
		}(int64(i))
	}
	wg.Wait()
	if pos, err := f.Seek(0, io.SeekCurrent); err != nil || pos != 0 {
		t.Fatalf("ReaderAt changed cursor: %d %v", pos, err)
	}
}

const expectedContainerXML = "<container><rootfile full-path=\"book.opf\"/></container>"

func zipBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, err := z.CreateHeader(&zip.FileHeader{Name: "META-INF/container.xml", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte(expectedContainerXML)); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func assertZIP(t *testing.T, f File) {
	t.Helper()
	z, err := zip.NewReader(f, f.Info().Size)
	if err != nil {
		t.Fatal(err)
	}
	r, err := z.Open("META-INF/container.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil || string(b) != expectedContainerXML {
		t.Fatalf("ZIP content: %q %v", b, err)
	}
}
func TestZIPReaderUsesNativeRanges(t *testing.T) {
	f, err := Open(context.Background(), &rangeSource{data: zipBytes(t)}, bookRef)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	assertZIP(t, f)
}

type blockedSource struct{ started chan struct{} }

func (s blockedSource) Stat(context.Context, Ref) (Info, error) {
	return Info{Name: "book", Revision: "v1", Size: 10}, nil
}
func (s blockedSource) ReadRange(ctx context.Context, _ Ref, _, _ int64, _ io.Writer) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}
func TestCloseAndContextCancelUnblockReads(t *testing.T) {
	for _, closeFile := range []bool{true, false} {
		t.Run(map[bool]string{true: "close", false: "context"}[closeFile], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := blockedSource{started: make(chan struct{})}
			f, err := Open(ctx, s, bookRef)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			done := make(chan error, 1)
			go func() { _, err := f.ReadAt(make([]byte, 1), 0); done <- err }()
			select {
			case <-s.started:
			case <-time.After(3 * time.Second):
				t.Fatal("read did not start")
			}
			if closeFile {
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("read blocked after cancellation")
			}
			if _, err := f.ReadAt(make([]byte, 1), 0); err == nil {
				t.Fatal("read after cancellation succeeded")
			}
			if closeFile {
				if _, err := f.Seek(0, io.SeekStart); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("seek after close: %v", err)
				}
			}
		})
	}
}

func TestLocalReaderPreservesBytesAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	data := zipBytes(t)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f, err := OpenLocal(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	assertZIP(t, f)
	cancel()
	// Context cancellation must reject further reads even before async cleanup.
	if _, err = f.ReadAt(make([]byte, 1), 0); err == nil {
		t.Fatal("local read ignored cancellation")
	}
	if _, err = OpenLocal(context.Background(), t.TempDir()); err == nil {
		t.Fatal("directory opened as media")
	}
}

// This fixture is a hostile RPC peer. The real adapter must reject bad streams.
type rpcStorage struct {
	storagev1.UnimplementedStorageProviderServer
	mode    string
	started chan struct{}
}

func (s *rpcStorage) Stat(_ context.Context, r *storagev1.StatRequest) (*storagev1.StatResponse, error) {
	if r.GetExpectedRevision() != "v1" {
		return nil, status.Error(codes.FailedPrecondition, "revision")
	}
	e := &storagev1.Entry{Id: r.GetEntryId(), Name: "book.epub", Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 10, Revision: "v1"}
	switch s.mode {
	case "changed-stat":
		e.Revision = "v2"
	case "wrong-id":
		e.Id = "other"
	case "directory":
		e.Kind = storagev1.EntryKind_ENTRY_KIND_DIRECTORY
	case "negative-size":
		e.Size = -1
	case "nil-stat":
		return &storagev1.StatResponse{}, nil
	}
	return &storagev1.StatResponse{Entry: e}, nil
}
func (s *rpcStorage) Read(r *storagev1.ReadRequest, stream storagev1.StorageProvider_ReadServer) error {
	if r.GetExpectedRevision() != "v1" {
		return status.Error(codes.FailedPrecondition, "unpinned")
	}
	if s.mode == "block" {
		close(s.started)
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	if s.mode == "changed-read" {
		return status.Error(codes.FailedPrecondition, "revision")
	}
	chunk := &storagev1.ReadChunk{Offset: r.Offset, Data: []byte("0123456789")[r.Offset : r.Offset+r.Length], Eof: true}
	switch s.mode {
	case "gap":
		chunk.Offset++
	case "too-many":
		chunk.Data = append(bytes.Clone(chunk.Data), 'x')
	case "large-chunk":
		chunk.Data = make([]byte, (128<<10)+1)
	case "short":
		chunk.Data = chunk.Data[:len(chunk.Data)-1]
	case "empty":
		chunk.Data = nil
		chunk.Eof = false
	case "no-terminal":
		chunk.Eof = false
	case "duplicate":
		if err := stream.Send(chunk); err != nil {
			return err
		}
	case "trailer-error":
		if err := stream.Send(chunk); err != nil {
			return err
		}
		return status.Error(codes.FailedPrecondition, "changed after data")
	}
	return stream.Send(chunk)
}
func rpcClient(t *testing.T, server storagev1.StorageProviderServer) storagev1.StorageProviderClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	storagev1.RegisterStorageProviderServer(srv, server)
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	t.Cleanup(func() { _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///storage", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return storagev1.NewStorageProviderClient(conn)
}
func TestPluginRejectsMalformedStatAndRead(t *testing.T) {
	for _, mode := range []string{"changed-stat", "wrong-id", "directory", "negative-size", "nil-stat", "gap", "too-many", "large-chunk", "short", "empty", "no-terminal", "duplicate", "trailer-error", "changed-read"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			source := NewPluginSource(rpcClient(t, &rpcStorage{mode: mode}))
			f, err := Open(ctx, source, bookRef)
			if err == nil {
				defer f.Close()
				_, err = f.ReadAt(make([]byte, 10), 0)
			}
			if err == nil {
				t.Fatal("malformed provider accepted")
			}
			if mode == "changed-read" || mode == "trailer-error" {
				if status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("revision error lost: %v", err)
				}
			}
		})
	}
}
func TestPluginPinnedReadAndCloseCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f, err := Open(ctx, NewPluginSource(rpcClient(t, &rpcStorage{})), bookRef)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := make([]byte, 3)
	n, err := f.ReadAt(b, 4)
	if err != nil || n != 3 || string(b) != "456" {
		t.Fatalf("RPC read: %d %q %v", n, b, err)
	}
	s := &rpcStorage{mode: "block", started: make(chan struct{})}
	blocked, err := Open(ctx, NewPluginSource(rpcClient(t, s)), bookRef)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	done := make(chan error, 1)
	go func() { _, err := blocked.ReadAt(make([]byte, 1), 0); done <- err }()
	select {
	case <-s.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_ = blocked.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled RPC succeeded")
		}
	case <-ctx.Done():
		t.Fatal("RPC cancellation blocked")
	}
}

func TestOpenRejectsInvalidReferences(t *testing.T) {
	for _, ref := range []Ref{{}, {SourceID: "fixture", EntryID: "book"}, {SourceID: "fixture", EntryID: "book", Revision: "v2"}, {SourceID: "\xff", EntryID: "book", Revision: "v1"}} {
		if f, err := Open(context.Background(), &rangeSource{data: []byte("x")}, ref); err == nil {
			_ = f.Close()
			t.Fatalf("bad ref accepted: %#v", ref)
		}
	}
}

// io.ReadFull discards a simultaneous error when n equals the requested size.
// A failed final RPC status must therefore never commit the entire range.
func TestFailedRangeCannotLookSuccessfulToReadFull(t *testing.T) {
	for _, mode := range []string{"trailer-error", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			f, err := Open(ctx, NewPluginSource(rpcClient(t, &rpcStorage{mode: mode})), bookRef)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			b := make([]byte, 10)
			n, err := io.ReadFull(f, b)
			if err == nil || n != 0 {
				t.Fatalf("failed range reported as successful: n=%d err=%v", n, err)
			}
			if !bytes.Equal(b, make([]byte, 10)) {
				t.Fatal("failed range left unvalidated bytes in caller buffer")
			}
		})
	}
}
