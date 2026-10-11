package transcodenode

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func nativeDeliveryFixture(t *testing.T) (*Server, *NativeExecutionGrant) {
	t.Helper()
	server := newTestServer(t)
	card := playback.NewRecipeCard(1, "profile", 42, "", playback.TranscodeOpts{SessionID: "player", TranscodeTransportID: "transport", InputPath: nativeExecutionTestLocation, TargetCodecVideo: "h264", TargetCodecAudio: "copy", SegmentDuration: 6})
	server.SetRecipeStore(&stubRecipeStore{card: &card, ok: true})
	source := &playback.TranscodeInputSource{Open: func(context.Context, playback.TranscodeOpts) (playback.TranscodeInput, error) {
		t.Error("artifact delivery attempted tool input or probe")
		return nil, errors.New("unexpected tool input")
	}}
	grant := &NativeExecutionGrant{WorkerID: 7, PlaybackSessionID: "player", TransportID: "transport", InputPath: nativeExecutionTestLocation, Revision: "immutable-version", InputFormat: "mov", InputSource: source, ExpiresAt: time.Now().Add(time.Minute)}
	return server, grant
}

func nativeDeliveryRequest(method string) *http.Request {
	r := httptest.NewRequest(method, "/transcode/transport/segment/0.ts", nil)
	r.Header.Set("Authorization", "Bearer "+testSecret)
	return r
}

func TestNativeDeliveryRequiresRetainedAuthority(t *testing.T) {
	server, grant := nativeDeliveryFixture(t)
	wrapped := server.WrapNativeExecution(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("private-native-artifact"))
	}), NativeExecutionConfig{NodeID: 7, Resolve: func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) { return grant, nil }})
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, nativeDeliveryRequest(http.MethodGet))
	if w.Code != http.StatusServiceUnavailable || bytes.Contains(w.Body.Bytes(), []byte("private-native-artifact")) {
		t.Fatalf("native artifact bypassed missing delivery authority: %d %q", w.Code, w.Body.String())
	}
}

type nativeDeliveryOwner struct {
	revoked   atomic.Bool
	cancelled atomic.Bool
	closed    atomic.Int32
	failure   error
	closeErr  error
}

func (o *nativeDeliveryOwner) Reauthorize(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.revoked.Load() {
		return o.failure
	}
	return nil
}
func (o *nativeDeliveryOwner) Cancel() { o.cancelled.Store(true) }
func (o *nativeDeliveryOwner) Close() error {
	o.closed.Add(1)
	return o.closeErr
}

type nativeDeliveryWriter struct {
	*httptest.ResponseRecorder
	afterFirst func()
	once       sync.Once
}

func (w *nativeDeliveryWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if n > 0 {
		w.once.Do(w.afterFirst)
	}
	return n, err
}

func runNativeDelivery(handler http.Handler, w http.ResponseWriter, r *http.Request) (aborted any) {
	defer func() { aborted = recover() }()
	handler.ServeHTTP(w, r)
	return nil
}

func TestNativeDeliveryWithdrawsSecondChunk(t *testing.T) {
	for _, cause := range []string{"grant-revoked", "source-denied", "redis-failed", "request-cancelled", "expired"} {
		t.Run(cause, func(t *testing.T) {
			server, grant := nativeDeliveryFixture(t)
			owner := &nativeDeliveryOwner{failure: errors.New(cause)}
			var opened, resolved atomic.Int32
			grant.OpenDelivery = func(context.Context) (NativeDeliveryAuthority, error) {
				opened.Add(1)
				return owner, nil
			}
			r := nativeDeliveryRequest(http.MethodGet)
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			r = r.WithContext(ctx)
			var active context.Context
			w := &nativeDeliveryWriter{ResponseRecorder: httptest.NewRecorder(), afterFirst: func() {
				switch cause {
				case "request-cancelled":
					cancel()
				case "expired":
					if _, ok := active.Deadline(); !ok {
						t.Error("response has no frozen grant deadline")
						cancel()
					}
					<-active.Done()
				default:
					owner.revoked.Store(true)
				}
			}}
			wrapped := server.WrapNativeExecution(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				active = r.Context()
				if _, bypass := w.(io.ReaderFrom); bypass {
					t.Error("native delivery exposes a ReaderFrom bypass")
				}
				w.Header().Set("Content-Type", "video/mp2t")
				_, _ = w.Write([]byte("first"))
				_, _ = w.Write([]byte("second"))
			}), NativeExecutionConfig{NodeID: 7, Resolve: func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) {
				resolved.Add(1)
				if cause == "expired" {
					grant.ExpiresAt = time.Now().Add(100 * time.Millisecond)
				}
				return grant, nil
			}})
			aborted := runNativeDelivery(wrapped, w, r)
			//nolint:errorlint // net/http requires this exact panic value to suppress abort logging.
			if aborted != http.ErrAbortHandler || w.Body.String() != "first" {
				t.Fatalf("withdrawn artifact retained body: abort=%v body=%q", aborted, w.Body.String())
			}
			if resolved.Load() != 1 || opened.Load() != 1 || owner.closed.Load() != 1 || !owner.cancelled.Load() {
				t.Fatalf("delivery did not retain/join one owner: resolves=%d opens=%d closes=%d cancelled=%v", resolved.Load(), opened.Load(), owner.closed.Load(), owner.cancelled.Load())
			}
		})
	}
}

func TestNativeDeliveryChecksHeadersAndPreservesErrors(t *testing.T) {
	for _, scenario := range []string{"head", "conditional", "denied-head", "denied-conditional", "not-found", "close-failed"} {
		t.Run(scenario, func(t *testing.T) {
			server, grant := nativeDeliveryFixture(t)
			owner := &nativeDeliveryOwner{failure: errors.New("source denied")}
			if scenario == "close-failed" {
				owner.closeErr = errors.New("source close failed")
			}
			grant.OpenDelivery = func(context.Context) (NativeDeliveryAuthority, error) { return owner, nil }
			method := http.MethodGet
			if scenario == "head" || scenario == "denied-head" {
				method = http.MethodHead
			}
			wrapped := server.WrapNativeExecution(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				switch scenario {
				case "head":
					w.Header().Set("Content-Length", "8")
					w.WriteHeader(http.StatusOK)
				case "conditional":
					w.WriteHeader(http.StatusNotModified)
				case "denied-head", "denied-conditional":
					owner.revoked.Store(true)
					w.Header().Set("Content-Length", "8")
					if scenario == "denied-head" {
						w.WriteHeader(http.StatusOK)
					} else {
						w.WriteHeader(http.StatusNotModified)
					}
				case "not-found":
					owner.revoked.Store(true)
					http.Error(w, "original missing segment", http.StatusNotFound)
				case "close-failed":
					_, _ = w.Write([]byte("artifact"))
				}
			}), NativeExecutionConfig{NodeID: 7, Resolve: func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) { return grant, nil }})
			w := httptest.NewRecorder()
			aborted := runNativeDelivery(wrapped, w, nativeDeliveryRequest(method))
			switch scenario {
			case "head":
				if w.Code != http.StatusOK || w.Body.Len() != 0 {
					t.Fatalf("HEAD %d %q", w.Code, w.Body.String())
				}
			case "conditional":
				if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
					t.Fatalf("conditional %d %q", w.Code, w.Body.String())
				}
			case "denied-head", "denied-conditional":
				if w.Code != http.StatusForbidden || w.Header().Get("Content-Length") == "8" || scenario == "denied-head" && w.Body.Len() != 0 {
					t.Fatalf("denied headers %d %v %q", w.Code, w.Header(), w.Body.String())
				}
			case "not-found":
				if w.Code != http.StatusNotFound || w.Body.String() != "original missing segment\n" {
					t.Fatalf("ordinary error changed: %d %q", w.Code, w.Body.String())
				}
			}
			//nolint:errorlint // net/http requires this exact panic value to suppress abort logging.
			if scenario == "close-failed" && aborted != http.ErrAbortHandler || scenario != "close-failed" && aborted != nil {
				t.Fatalf("unexpected response abort: %v", aborted)
			}
			if owner.closed.Load() != 1 || !owner.cancelled.Load() {
				t.Fatal("header/error delivery owner was not joined")
			}
		})
	}
}

func TestNativeDeliveryLeavesOrdinaryWorkerPathsUntouched(t *testing.T) {
	server := newTestServer(t)
	wrapped := server.WrapNativeExecution(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ordinary artifact"))
	}), NativeExecutionConfig{NodeID: 7, Resolve: func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) {
		t.Error("ordinary path reached native resolution")
		return nil, errors.New("unexpected native resolver")
	}})
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, nativeDeliveryRequest(http.MethodGet))
	if w.Code != http.StatusOK || w.Body.String() != "ordinary artifact" {
		t.Fatalf("ordinary response changed: %d %q", w.Code, w.Body.String())
	}
}

func TestNativeDeliveryCancellationInterruptsBlockedWrite(t *testing.T) {
	for _, cause := range []string{"request", "expiry"} {
		t.Run(cause, func(t *testing.T) {
			server, grant := nativeDeliveryFixture(t)
			owner := &nativeDeliveryOwner{failure: errors.New("authority denied")}
			grant.OpenDelivery = func(context.Context) (NativeDeliveryAuthority, error) { return owner, nil }
			wrapped := server.WrapNativeExecution(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("blocked artifact"))
			}), NativeExecutionConfig{NodeID: 7, Resolve: func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) {
				if cause == "expiry" {
					grant.ExpiresAt = time.Now().Add(100 * time.Millisecond)
				}
				return grant, nil
			}})
			w := newAbortableBlockingResponseWriter()
			r := nativeDeliveryRequest(http.MethodGet)
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			result := make(chan any, 1)
			go func() { result <- runNativeDelivery(wrapped, w, r.WithContext(ctx)) }()
			waitCtx, stop := context.WithTimeout(t.Context(), 3*time.Second)
			defer stop()
			select {
			case <-w.writeStarted:
			case aborted := <-result:
				t.Fatalf("response ended before blocked write: %v", aborted)
			case <-waitCtx.Done():
				_ = w.SetWriteDeadline(time.Now())
				t.Fatal("artifact did not reach write")
			}
			if cause == "request" {
				cancel()
			}
			select {
			case aborted := <-result:
				//nolint:errorlint // net/http requires this exact panic value to suppress abort logging.
				if aborted != http.ErrAbortHandler || !owner.cancelled.Load() || owner.closed.Load() != 1 {
					t.Fatalf("blocked write did not join after cancellation: %v cancel=%v close=%d", aborted, owner.cancelled.Load(), owner.closed.Load())
				}
			case <-waitCtx.Done():
				_ = w.SetWriteDeadline(time.Now())
				<-result
				t.Fatal("cancellation did not interrupt blocked artifact write")
			}
		})
	}
}

type nativeDeliveryFailedFlush struct{ *httptest.ResponseRecorder }

func (*nativeDeliveryFailedFlush) FlushError() error { return errors.New("socket flush failed") }

func TestNativeDeliveryFailedFlushAbortsFramedResponse(t *testing.T) {
	server, grant := nativeDeliveryFixture(t)
	owner := &nativeDeliveryOwner{}
	grant.OpenDelivery = func(context.Context) (NativeDeliveryAuthority, error) { return owner, nil }
	wrapped := server.WrapNativeExecution(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("artifact"))
		w.(http.Flusher).Flush()
	}), NativeExecutionConfig{NodeID: 7, Resolve: func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) { return grant, nil }})
	w := &nativeDeliveryFailedFlush{httptest.NewRecorder()}
	//nolint:errorlint // net/http requires this exact panic value to suppress abort logging.
	if aborted := runNativeDelivery(wrapped, w, nativeDeliveryRequest(http.MethodGet)); aborted != http.ErrAbortHandler {
		t.Fatalf("failed successful flush did not abort: %v", aborted)
	}
	if !owner.cancelled.Load() || owner.closed.Load() != 1 {
		t.Fatal("failed flush did not cancel and join its source owner")
	}
}
