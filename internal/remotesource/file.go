package remotesource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
)

// BlockBytes is the first read and the smallest range File fetches.
const BlockBytes = 1 << 20

var (
	// ErrRangeUnsupported is a remote server that answers a range request
	// with the whole body, or with no total size, for media larger than one
	// block. Seeking needs ranges.
	ErrRangeUnsupported = errors.New("remote source: the server does not answer range requests")
	// ErrChanged is a remote resource whose size or ETag changed while open.
	ErrChanged = errors.New("remote source: the remote media changed")
	errClosed  = errors.New("remote source: file closed")
)

// HTTPStatusError is a remote answer other than 200 or 206. It names the
// status only, never the URL.
type HTTPStatusError struct{ Status int }

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("remote source: the server answered HTTP %d", e.Status)
}

// File reads one remote URL as a seekable file with HTTP range requests
// through a guarded client. It implements mediasource.File plus the Err,
// Cancel and Context methods Bloem's read-ahead and tool bridge use. Every
// range checks the total size and the ETag seen at Open.
type File struct {
	ctx    context.Context
	cancel context.CancelFunc
	client *http.Client
	url    string
	info   mediasource.Info
	etag   string

	mu       sync.Mutex
	block    []byte
	blockOff int64
	pos      int64
	sticky   error
	closed   bool
}

var _ mediasource.File = (*File)(nil)

// Open fetches the first block. name is a container hint (see NameHint).
func Open(ctx context.Context, client *http.Client, rawURL, name string) (*File, error) {
	if ctx == nil || client == nil {
		return nil, errors.New("remote source: no client")
	}
	if err := ValidateURL(rawURL); err != nil {
		return nil, err
	}
	fileCtx, cancel := context.WithCancel(ctx)
	f := &File{ctx: fileCtx, cancel: cancel, client: client, url: rawURL, info: mediasource.Info{Name: name, LogicalPath: name, Revision: Revision(rawURL), Size: -1}}
	data, header, err := f.fetch(fileCtx, 0, BlockBytes)
	if err != nil {
		cancel()
		return nil, err
	}
	f.etag = header.etag
	f.info.Size = header.total
	f.info.ModifiedAt = header.modified
	f.block, f.blockOff = data, 0
	return f, nil
}

type fetchHeader struct {
	total    int64
	etag     string
	modified time.Time
}

// fetch reads [offset, offset+length) clipped to the total size.
func (f *File) fetch(ctx context.Context, offset, length int64) ([]byte, fetchHeader, error) {
	var header fetchHeader
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, header, ErrInvalidStrm
	}
	req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-"+strconv.FormatInt(offset+length-1, 10))
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, header, scrubURL(err)
	}
	defer resp.Body.Close()
	header.etag = resp.Header.Get("ETag")
	if modified, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		header.modified = modified.UTC()
	}
	switch resp.StatusCode {
	case http.StatusPartialContent:
		start, end, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != offset || end < start || total <= 0 {
			return nil, header, ErrRangeUnsupported
		}
		header.total = total
		want := end - start + 1
		if want > length {
			want = length
		}
		data := make([]byte, want)
		if _, err := io.ReadFull(resp.Body, data); err != nil {
			return nil, header, scrubURL(err)
		}
		return data, header, nil
	case http.StatusOK:
		// The whole body: usable only from the start, and only when it is
		// all we asked for.
		if offset != 0 || resp.ContentLength < 0 || resp.ContentLength > length {
			return nil, header, ErrRangeUnsupported
		}
		header.total = resp.ContentLength
		data := make([]byte, resp.ContentLength)
		if _, err := io.ReadFull(resp.Body, data); err != nil {
			return nil, header, scrubURL(err)
		}
		return data, header, nil
	case http.StatusRequestedRangeNotSatisfiable:
		return nil, header, io.EOF
	default:
		return nil, header, &HTTPStatusError{Status: resp.StatusCode}
	}
}

// scrubURL drops the URL that net/http puts into *url.Error.
func scrubURL(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	if errors.Is(urlErr.Err, ErrBlockedAddress) {
		return ErrBlockedAddress
	}
	return fmt.Errorf("remote source: request failed: %w", urlErr.Err)
}

func parseContentRange(value string) (start, end, total int64, ok bool) {
	value, found := strings.CutPrefix(strings.TrimSpace(value), "bytes ")
	if !found {
		return 0, 0, 0, false
	}
	span, size, found := strings.Cut(value, "/")
	if !found || size == "*" {
		return 0, 0, 0, false
	}
	first, last, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, 0, false
	}
	var err error
	if start, err = strconv.ParseInt(first, 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if end, err = strconv.ParseInt(last, 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if total, err = strconv.ParseInt(size, 10, 64); err != nil || end >= total {
		return 0, 0, 0, false
	}
	return start, end, total, true
}

// Info reports the URL revision and the remote size.
func (f *File) Info() mediasource.Info { return f.info }

// Context ends when the file is closed or cancelled.
func (f *File) Context() context.Context { return f.ctx }

// Cancel stops reads in flight; Close still releases the file.
func (f *File) Cancel() { f.cancel() }

// Err is sticky: a changed resource or a failed read fails every later read.
func (f *File) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sticky != nil {
		return f.sticky
	}
	return f.ctx.Err()
}

func (f *File) fail(err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	f.mu.Lock()
	if f.sticky == nil {
		f.sticky = err
	}
	f.mu.Unlock()
	return err
}

// ReadAt reads from the cached block when it covers the request, otherwise
// fetches at least one block from offset.
func (f *File) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errors.New("remote source: negative offset")
	}
	if err := f.Err(); err != nil {
		return 0, err
	}
	if offset >= f.info.Size {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	f.mu.Lock()
	if offset >= f.blockOff && offset < f.blockOff+int64(len(f.block)) {
		n := copy(p, f.block[offset-f.blockOff:])
		f.mu.Unlock()
		if n == len(p) || offset+int64(n) >= f.info.Size {
			return n, eofAt(offset+int64(n), f.info.Size, n, len(p))
		}
		m, err := f.ReadAt(p[n:], offset+int64(n))
		return n + m, err
	}
	f.mu.Unlock()
	length := max(int64(len(p)), BlockBytes)
	if remaining := f.info.Size - offset; length > remaining {
		length = remaining
	}
	data, header, err := f.fetch(f.ctx, offset, length)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return 0, f.fail(ErrChanged)
		}
		return 0, f.fail(err)
	}
	if header.total != f.info.Size || (f.etag != "" && header.etag != "" && header.etag != f.etag) {
		return 0, f.fail(ErrChanged)
	}
	if int64(len(data)) < length {
		return 0, f.fail(io.ErrUnexpectedEOF)
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return 0, errClosed
	}
	f.block, f.blockOff = data, offset
	f.mu.Unlock()
	n := copy(p, data)
	return n, eofAt(offset+int64(n), f.info.Size, n, len(p))
}

func eofAt(next, size int64, n, want int) error {
	if n < want && next >= size {
		return io.EOF
	}
	return nil
}

// Read reads at the current position.
func (f *File) Read(p []byte) (int, error) {
	f.mu.Lock()
	pos := f.pos
	f.mu.Unlock()
	n, err := f.ReadAt(p, pos)
	f.mu.Lock()
	f.pos = pos + int64(n)
	f.mu.Unlock()
	if err == nil && n == 0 && len(p) > 0 {
		return 0, io.EOF
	}
	return n, err
}

// Seek moves the read position.
func (f *File) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = f.pos + offset
	case io.SeekEnd:
		next = f.info.Size + offset
	default:
		return 0, errors.New("remote source: invalid whence")
	}
	if next < 0 {
		return 0, errors.New("remote source: negative position")
	}
	f.pos = next
	return next, nil
}

// Close cancels reads in flight and drops the cached block.
func (f *File) Close() error {
	f.cancel()
	f.mu.Lock()
	f.closed, f.block = true, nil
	f.mu.Unlock()
	return nil
}
