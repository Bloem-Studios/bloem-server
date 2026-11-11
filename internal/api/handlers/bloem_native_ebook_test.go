package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
)

type nativeReaderTestFile struct {
	*bytes.Reader
	info    mediasource.Info
	reads   int
	closed  bool
	failure error
}

func (f *nativeReaderTestFile) Info() mediasource.Info { return f.info }
func (f *nativeReaderTestFile) Close() error           { f.closed = true; return nil }
func (f *nativeReaderTestFile) Read(p []byte) (int, error) {
	f.reads++
	if f.failure != nil {
		return 0, f.failure
	}
	return f.Reader.Read(p)
}

type nativeReaderTestOpener struct {
	file       *nativeReaderTestFile
	err        error
	calls      int
	authorized *models.MediaFile
}

func (o *nativeReaderTestOpener) OpenAuthorizedEbook(_ context.Context, f *models.MediaFile) (mediasource.File, error) {
	o.calls++
	o.authorized = f
	return o.file, o.err
}
func nativeReaderTestSetup(t *testing.T) (*NativeEbookFileService, *nativeReaderTestOpener, *models.MediaFile) {
	t.Helper()
	f := &models.MediaFile{ID: 42, ContentID: "ebook-1", MediaFolderID: 7, FilePath: "bloem-storage:" + strings.Repeat("a", 64), Container: "epub", BaseType: "ebook"}
	reader := &nativeReaderTestFile{Reader: bytes.NewReader([]byte("0123456789")), info: mediasource.Info{Name: "book.epub", Revision: "revision-1", Size: 10}}
	opener := &nativeReaderTestOpener{file: reader}
	local := NewEbookReaderHandler(&MediaFileAuthorizer{FileResolver: stubMediaFileResolver{file: f}, ItemAccess: stubItemAccessChecker{}})
	return NewNativeEbookFileService(local, opener), opener, f
}

// readNativeEbookV2 makes the two service calls /api/v2's readEbookFile makes
// (internal/apiv2/ebook_file.go) and returns the error that operation renders
// as its problem response. Nothing is written to w for a failed resolve.
func readNativeEbookV2(h *NativeEbookFileService, w http.ResponseWriter, r *http.Request) error {
	fileID, err := strconv.Atoi(chi.URLParam(r, "file_id"))
	if err != nil {
		return err
	}
	file, err := h.ResolveReaderFile(r.Context(), chi.URLParam(r, "content_id"), fileID, AccessFilterFromContext(r.Context(), ""))
	if err != nil {
		return err
	}
	return h.ServeReaderFile(w, r, file)
}

// nativeEbookErrStatus is the status a typed service error carries, 404 for
// the catalog's not-found errors, and 0 for no error.
func nativeEbookErrStatus(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, catalog.ErrItemNotFound) || errors.Is(err, catalog.ErrEpisodeNotFound) {
		return http.StatusNotFound
	}
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return apiErr.Status
	}
	return http.StatusInternalServerError
}

func nativeReadRequest(method string) *http.Request {
	return withEbookReaderRouteParams(newEbookReaderAuthRequest(method, "/ebooks/ebook-1/files/42/read"), "ebook-1", "42")
}
func TestNativeEbookReaderGetHeadRangeAndConditional(t *testing.T) {
	for _, tc := range []struct {
		name, method, rangeValue string
		status                   int
		body                     string
	}{
		{"get", "GET", "", 200, "0123456789"}, {"head", "HEAD", "", 200, ""}, {"range", "GET", "bytes=2-5", 206, "2345"}, {"head range", "HEAD", "bytes=2-5", 206, ""}, {"invalid range", "GET", "bytes=20-22", 416, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, opener, _ := nativeReaderTestSetup(t)
			r := nativeReadRequest(tc.method)
			r.Header.Set("Range", tc.rangeValue)
			w := httptest.NewRecorder()
			if err := readNativeEbookV2(h, w, r); err != nil {
				t.Fatalf("read: %v", err)
			}
			if w.Code != tc.status {
				t.Fatalf("status %d, body %s", w.Code, w.Body.String())
			}
			if tc.status != 416 && w.Body.String() != tc.body {
				t.Fatalf("body %q, want %q", w.Body.String(), tc.body)
			}
			if tc.method == "HEAD" && opener.file.reads != 0 {
				t.Fatal("HEAD read body")
			}
			if !opener.file.closed {
				t.Fatal("file not closed")
			}
			if tc.status != 416 && (w.Header().Get("Content-Type") != "application/epub+zip" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("ETag") == "") {
				t.Fatalf("headers %v", w.Header())
			}
		})
	}
	h, _, _ := nativeReaderTestSetup(t)
	first := httptest.NewRecorder()
	if err := readNativeEbookV2(h, first, nativeReadRequest("HEAD")); err != nil {
		t.Fatal(err)
	}
	tag := first.Header().Get("ETag")
	for _, tc := range []struct {
		header, value string
		status        int
	}{{"If-None-Match", tag, 304}, {"If-Match", "\"wrong\"", 412}, {"If-Range", "\"wrong\"", 200}} {
		h, _, _ := nativeReaderTestSetup(t)
		r := nativeReadRequest("GET")
		r.Header.Set(tc.header, tc.value)
		if tc.header == "If-Range" {
			r.Header.Set("Range", "bytes=2-5")
		}
		w := httptest.NewRecorder()
		if err := readNativeEbookV2(h, w, r); err != nil {
			t.Fatalf("%s: %v", tc.header, err)
		}
		if w.Code != tc.status {
			t.Fatalf("%s status %d, want %d", tc.header, w.Code, tc.status)
		}
	}
}
func TestNativeEbookReaderMultipartRange(t *testing.T) {
	h, _, _ := nativeReaderTestSetup(t)
	r := nativeReadRequest("GET")
	r.Header.Set("Range", "bytes=0-1,8-9")
	w := httptest.NewRecorder()
	if err := readNativeEbookV2(h, w, r); err != nil {
		t.Fatal(err)
	}
	if w.Code != 206 {
		t.Fatalf("status %d", w.Code)
	}
	response := w.Result()
	defer response.Body.Close()
	boundary := strings.Split(response.Header.Get("Content-Type"), "boundary=")
	if len(boundary) != 2 {
		t.Fatalf("content type %s", response.Header.Get("Content-Type"))
	}
	m := multipart.NewReader(response.Body, boundary[1])
	for _, want := range []string{"01", "89"} {
		part, err := m.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(part)
		if err != nil || string(got) != want {
			t.Fatalf("part %q %v", got, err)
		}
	}
}
func TestNativeEbookReaderAuthorizationBeforeOpening(t *testing.T) {
	h, opener, file := nativeReaderTestSetup(t)
	h.Local.FileAuthorizer.ItemAccess = stubItemAccessChecker{err: catalog.ErrItemNotFound}
	err := readNativeEbookV2(h, httptest.NewRecorder(), nativeReadRequest("GET"))
	if nativeEbookErrStatus(err) != 404 || opener.calls != 0 {
		t.Fatalf("error %v opens %d", err, opener.calls)
	}
	h.Local.FileAuthorizer.ItemAccess = stubItemAccessChecker{}
	file.ContentID = "different"
	err = readNativeEbookV2(h, httptest.NewRecorder(), nativeReadRequest("GET"))
	if nativeEbookErrStatus(err) != 404 || opener.calls != 0 {
		t.Fatalf("mismatch error %v opens %d", err, opener.calls)
	}
}
func TestNativeEbookReaderUnavailableAndUnsupportedNeverFallBack(t *testing.T) {
	h, opener, file := nativeReaderTestSetup(t)
	opener.err = errors.New("provider down")
	err := readNativeEbookV2(h, httptest.NewRecorder(), nativeReadRequest("GET"))
	if nativeEbookErrStatus(err) != 503 {
		t.Fatalf("error %v", err)
	}
	file.Container = "mobi"
	opener.err = nil
	opener.calls = 0
	err = readNativeEbookV2(h, httptest.NewRecorder(), nativeReadRequest("GET"))
	if nativeEbookErrStatus(err) != 415 || opener.calls != 0 {
		t.Fatalf("unsupported error %v opens %d", err, opener.calls)
	}
}

// A provider failure at open or before the first byte (for example a book
// whose file is missing in the backend) is still an ordinary HTTP error: no
// binary framing has reached the client yet, so it must not be an empty reply.
func TestNativeEbookReaderFirstReadFailureIsAnHTTPError(t *testing.T) {
	for _, tc := range []struct {
		name, rangeValue string
		open             bool
		failure          error
		status           int
	}{
		{"open missing", "", true, status.Error(codes.NotFound, "book absent"), 404},
		{"unavailable", "", false, errors.New("provider failure before any byte"), 503},
		{"missing", "", false, status.Error(codes.NotFound, "book file absent"), 404},
		{"missing range", "bytes=2-5", false, status.Error(codes.NotFound, "book file absent"), 404},
		{"unavailable range", "bytes=2-5", false, status.Error(codes.Unavailable, "backend down"), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, opener, _ := nativeReaderTestSetup(t)
			if tc.open {
				opener.err = tc.failure
			} else {
				opener.file.failure = tc.failure
			}
			r := nativeReadRequest("GET")
			r.Header.Set("Range", tc.rangeValue)
			w := httptest.NewRecorder()
			// Nothing has reached the client yet, so the error comes back for
			// the v2 operation to answer with a problem response.
			if err := readNativeEbookV2(h, w, r); nativeEbookErrStatus(err) != tc.status || w.Body.Len() != 0 {
				t.Fatalf("error %v (status %d), body %q", err, nativeEbookErrStatus(err), w.Body.String())
			}
			for _, header := range []string{"Accept-Ranges", "Content-Disposition", "Content-Length", "Content-Range", "ETag", "Last-Modified"} {
				if value := w.Header().Get(header); value != "" {
					t.Fatalf("error response kept the file's %s: %q", header, value)
				}
			}
			if (opener.file.reads == 0) != tc.open || !opener.file.closed {
				t.Fatalf("reads %d closed %v", opener.file.reads, opener.file.closed)
			}
		})
	}
}
func TestNativeEbookReaderLocalUnchanged(t *testing.T) {
	h, opener, file := nativeReaderTestSetup(t)
	file.FilePath = writePlaybackTestMediaFile(t, "book.epub")
	w := httptest.NewRecorder()
	if err := readNativeEbookV2(h, w, nativeReadRequest("GET")); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || opener.calls != 0 {
		t.Fatalf("local status %d opens %d", w.Code, opener.calls)
	}
}

type nativeReaderPartialFailure struct {
	*nativeReaderTestFile
	first   bool
	failure error
}

func (f *nativeReaderPartialFailure) Read(p []byte) (int, error) {
	if f.first {
		return 0, f.failure
	}
	f.first = true
	return f.nativeReaderTestFile.Read(p[:min(4, len(p))])
}

type nativeReaderFuncOpener func(context.Context, *models.MediaFile) (mediasource.File, error)

func (f nativeReaderFuncOpener) OpenAuthorizedEbook(ctx context.Context, m *models.MediaFile) (mediasource.File, error) {
	return f(ctx, m)
}
func TestNativeEbookReaderPartialBinaryNeverAppendsError(t *testing.T) {
	h, opener, _ := nativeReaderTestSetup(t)
	failing := &nativeReaderPartialFailure{nativeReaderTestFile: opener.file, failure: errors.New("after partial binary")}
	h.Native = nativeReaderFuncOpener(func(context.Context, *models.MediaFile) (mediasource.File, error) { return failing, nil })
	w := httptest.NewRecorder()
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler { //nolint:errorlint // net/http abort handling requires this exact panic sentinel.
			t.Fatalf("panic %v", recovered)
		}
		if w.Body.String() != "0123" {
			t.Fatalf("partial body %q", w.Body.String())
		}
	}()
	_ = readNativeEbookV2(h, w, nativeReadRequest("GET"))
}

type nativeReaderBlockingSource struct {
	started chan struct{}
	stopped chan struct{}
}

func (s *nativeReaderBlockingSource) Stat(context.Context, mediasource.Ref) (mediasource.Info, error) {
	return mediasource.Info{Name: "book.epub", Revision: "pinned", Size: 10}, nil
}
func (s *nativeReaderBlockingSource) ReadRange(ctx context.Context, _ mediasource.Ref, _, _ int64, _ io.Writer) error {
	close(s.started)
	<-ctx.Done()
	close(s.stopped)
	return ctx.Err()
}
func TestNativeEbookReaderDisconnectCancelsSource(t *testing.T) {
	h, _, _ := nativeReaderTestSetup(t)
	source := &nativeReaderBlockingSource{started: make(chan struct{}), stopped: make(chan struct{})}
	h.Native = nativeReaderFuncOpener(func(ctx context.Context, _ *models.MediaFile) (mediasource.File, error) {
		return mediasource.Open(ctx, source, mediasource.Ref{SourceID: "source", EntryID: "file", Revision: "pinned"})
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := apimw.SetClaims(r.Context(), &auth.Claims{UserID: 1, Role: "user", TokenType: auth.TokenTypeAccess})
		ctx = apimw.SetProfileID(ctx, "profile-1")
		_ = readNativeEbookV2(h, w, withEbookReaderRouteParams(r.WithContext(ctx), "ebook-1", "42"))
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(req)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-source.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider read did not start")
	}
	cancel()
	select {
	case <-source.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnect did not cancel provider")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("client did not stop")
	}
}

// Over a real connection the deferred status line must still frame both a
// complete book and an error answered before the first byte.
func TestNativeEbookReaderFirstReadFailureOverHTTP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		status  int
		body    string
	}{
		{"complete", nil, 200, "0123456789"},
		{"missing", status.Error(codes.NotFound, "book file absent"), 404, ""},
		{"unavailable", errors.New("backend down"), 503, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, opener, _ := nativeReaderTestSetup(t)
			opener.file.failure = tc.failure
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := apimw.SetClaims(r.Context(), &auth.Claims{UserID: 1, Role: "user", TokenType: auth.TokenTypeAccess})
				ctx = apimw.SetProfileID(ctx, "profile-1")
				if err := readNativeEbookV2(h, w, withEbookReaderRouteParams(r.WithContext(ctx), "ebook-1", "42")); err != nil {
					// The v2 operation answers with a problem response.
					w.WriteHeader(nativeEbookErrStatus(err))
				}
			}))
			defer server.Close()
			response, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal("response framing", err)
			}
			if response.StatusCode != tc.status || !strings.Contains(string(body), tc.body) {
				t.Fatalf("status %d body %q", response.StatusCode, body)
			}
		})
	}
}
