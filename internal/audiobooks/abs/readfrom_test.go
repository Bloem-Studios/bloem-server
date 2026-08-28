package abs

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/compatgateway"
	"github.com/Silo-Server/silo-server/internal/httpstream"
)

type readerFromSpy struct{ bytes.Buffer }

func (w *readerFromSpy) Header() http.Header { return make(http.Header) }
func (w *readerFromSpy) WriteHeader(int)     {}
func (w *readerFromSpy) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(&w.Buffer, r)
}

func TestStatusRecorderPreservesReaderFromAndAccounting(t *testing.T) {
	spy := &readerFromSpy{}
	w := &statusRecorder{ResponseWriter: spy}
	n, err := w.ReadFrom(bytes.NewBufferString("media"))
	if err != nil || n != 5 || w.bytes != 5 || w.status != http.StatusOK || spy.String() != "media" {
		t.Fatalf("ReadFrom = n=%d err=%v status=%d bytes=%d body=%q", n, err, w.status, w.bytes, spy.String())
	}
	if w.Unwrap() != spy {
		t.Fatal("Unwrap did not return underlying writer")
	}
}

type absWriterChainSpy struct {
	bytes.Buffer
	header            http.Header
	readFromCalled    bool
	flushed           bool
	writeDeadline     time.Time
	writeDeadlineCall int
}

func (w *absWriterChainSpy) Header() http.Header { return w.header }
func (w *absWriterChainSpy) WriteHeader(int)     {}
func (w *absWriterChainSpy) ReadFrom(r io.Reader) (int64, error) {
	w.readFromCalled = true
	return io.Copy(&w.Buffer, r)
}
func (w *absWriterChainSpy) Flush() { w.flushed = true }
func (w *absWriterChainSpy) SetWriteDeadline(deadline time.Time) error {
	w.writeDeadline = deadline
	w.writeDeadlineCall++
	return nil
}

func TestMountedABSMediaWriterChainPreservesStreamingInterfaces(t *testing.T) {
	t.Setenv("SILO_STREAM_WRITE_STALL_TIMEOUT", "1")

	base := &absWriterChainSpy{header: make(http.Header)}
	registry := telemetryRegistry(t)
	h := New(Dependencies{MediaStore: noopMediaStore{}})
	application := chi.NewRouter()
	application.Use(h.publicMountMiddleware)
	application.Use(h.accessLog)
	const pattern = "/public/session/{sid}/track/{idx}"

	wantDeadline := time.Unix(123, 456)
	var handlerErr error
	responseControllerReachedBase := false
	application.Get(pattern, observeABS(registry, http.MethodGet, pattern, func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(io.ReaderFrom); !ok {
			handlerErr = errMissingWriterInterface("io.ReaderFrom")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			handlerErr = errMissingWriterInterface("http.Flusher")
			return
		}
		if _, ok := w.(interface{ Unwrap() http.ResponseWriter }); !ok {
			handlerErr = errMissingWriterInterface("Unwrap")
			return
		}
		if !writerChainContains[*httpstream.RollingDeadlineWriter](w) {
			handlerErr = errMissingWriterInterface("mounted RollingDeadlineWriter")
			return
		}
		if !writerChainContains[*statusRecorder](w) {
			handlerErr = errMissingWriterInterface("ABS statusRecorder")
			return
		}
		if err := http.NewResponseController(w).SetWriteDeadline(wantDeadline); err != nil {
			handlerErr = err
			return
		}
		if base.writeDeadline != wantDeadline {
			handlerErr = writerInterfaceError("response-controller deadline support")
			return
		}
		responseControllerReachedBase = true
		flusher.Flush()
		_, handlerErr = w.(io.ReaderFrom).ReadFrom(bytes.NewBufferString("media"))
	}))

	gateway := compatgateway.New(compatgateway.Config{
		IdentitySecret: []byte("gateway-writer-chain-test"),
		LocalHandlers: map[compatgateway.AppKind]http.Handler{
			compatgateway.KindAudiobookshelf: application,
		},
	})
	gateway.ServeHTTP(base, httptest.NewRequest(http.MethodGet, "http://media.example/audiobookshelf/public/session/session-1/track/1", nil))

	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if !base.readFromCalled || base.String() != "media" {
		t.Fatalf("underlying ReaderFrom called=%t body=%q, want true and media", base.readFromCalled, base.String())
	}
	if !base.flushed {
		t.Fatal("Flush did not reach the underlying gateway writer")
	}
	if !responseControllerReachedBase {
		t.Fatal("response-controller deadline did not reach the underlying gateway writer")
	}
	if base.writeDeadlineCall < 2 {
		t.Fatalf("SetWriteDeadline calls = %d, want mounted enrollment plus response-controller probe", base.writeDeadlineCall)
	}
}

type writerInterfaceError string

func (e writerInterfaceError) Error() string { return "mounted ABS writer chain lost " + string(e) }

func errMissingWriterInterface(name string) error { return writerInterfaceError(name) }

func writerChainContains[T http.ResponseWriter](w http.ResponseWriter) bool {
	for {
		if _, ok := w.(T); ok {
			return true
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return false
		}
		next := unwrapper.Unwrap()
		if next == nil || next == w {
			return false
		}
		w = next
	}
}
