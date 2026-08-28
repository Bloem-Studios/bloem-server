package abs

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/compatgateway"
	"github.com/Silo-Server/silo-server/internal/httpstream"
)

const (
	gatewayTestWriteTimeout = 200 * time.Millisecond
	gatewayTestStallWindow  = time.Second
)

// TestMountedABSTrackRollingDeadlineSurvivesPublicWriteTimeout catches removal of the
// mounted-media enrollment. Each ReadFrom slice finishes inside the injected
// stall window, but the complete transfer takes longer than that window and
// the public server's absolute WriteTimeout. Only a deadline advanced after
// the first successful slice can carry the response to completion.
func TestMountedABSTrackRollingDeadlineSurvivesPublicWriteTimeout(t *testing.T) {
	t.Setenv("SILO_STREAM_WRITE_STALL_TIMEOUT", "1")

	type readFromResult struct {
		bytes int64
		err   error
	}
	const total = 2 * httpstream.ReadFromChunkDefault
	handlerDone := make(chan readFromResult, 1)
	server := mountedABSGatewayServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n, err := w.(io.ReaderFrom).ReadFrom(&pacedGatewayTrackReader{
			remaining: total,
			slicePace: 600 * time.Millisecond,
		})
		handlerDone <- readFromResult{bytes: n, err: err}
	}), gatewayTestWriteTimeout)

	response, err := server.Client().Get(server.URL + "/audiobookshelf/public/session/session-1/track/1")
	if err != nil {
		t.Fatalf("GET mounted ABS track: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	bytesRead, err := io.Copy(io.Discard, response.Body)
	if err != nil {
		t.Fatalf("mounted ABS track died before completing %d bytes: read %d: %v", total, bytesRead, err)
	}
	if bytesRead != total {
		t.Fatalf("mounted ABS track body = %d bytes, want %d", bytesRead, total)
	}
	if result := <-handlerDone; result.err != nil || result.bytes != total {
		t.Fatalf("handler ReadFrom = %d bytes, err %v; want %d bytes", result.bytes, result.err, total)
	}
}

// TestMountedABSTrackRollingDeadlineReapsStall catches a fallback to the public
// server's shorter absolute WriteTimeout. The client reads the response line
// and then stops consuming the body; the write must fail against the injected
// rolling stall window, not the original server deadline.
func TestMountedABSTrackRollingDeadlineReapsStall(t *testing.T) {
	t.Setenv("SILO_STREAM_WRITE_STALL_TIMEOUT", "1")

	type writeResult struct {
		elapsed time.Duration
		err     error
	}
	handlerDone := make(chan writeResult, 1)
	server := mountedABSGatewayServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started := time.Now()
		w.WriteHeader(http.StatusOK)
		block := make([]byte, 1<<20)
		var err error
		for range 256 {
			if _, err = w.Write(block); err != nil {
				break
			}
			w.(http.Flusher).Flush()
		}
		handlerDone <- writeResult{elapsed: time.Since(started), err: err}
	}), gatewayTestWriteTimeout)

	address := strings.TrimPrefix(server.URL, "http://")
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatalf("dial mounted ABS server: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.SetReadBuffer(1024); err != nil {
			t.Fatalf("set client read buffer: %v", err)
		}
	}
	if _, err := fmt.Fprintf(conn, "GET /audiobookshelf/public/session/session-1/track/1 HTTP/1.1\r\nHost: media.example\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if line, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
		t.Fatalf("read response status: %v", err)
	} else if !strings.Contains(line, " 200 ") {
		t.Fatalf("response status line = %q, want 200", line)
	}

	watchdog := time.NewTimer(5 * gatewayTestStallWindow)
	defer watchdog.Stop()
	select {
	case result := <-handlerDone:
		if result.err == nil {
			t.Fatal("non-reading client accepted the entire response without a write failure")
		}
		if outcome := httpstream.ClassifyOutcome(result.err, nil); outcome != httpstream.OutcomeStalledReap {
			t.Fatalf("stalled write outcome = %q for %v, want %q", outcome, result.err, httpstream.OutcomeStalledReap)
		}
		if result.elapsed < gatewayTestStallWindow/2 {
			t.Fatalf("stalled write failed after %s, before the %s rolling window; public WriteTimeout still controls it", result.elapsed, gatewayTestStallWindow)
		}
	case <-watchdog.C:
		t.Fatal("stalled mounted ABS client was not reaped")
	}
}

func mountedABSGatewayServer(t *testing.T, mediaHandler http.Handler, writeTimeout time.Duration) *httptest.Server {
	t.Helper()

	h := New(Dependencies{MediaStore: noopMediaStore{}})
	application := chi.NewRouter()
	application.Use(h.publicMountMiddleware)
	application.Use(h.accessLog)
	const pattern = "/public/session/{sid}/track/{idx}"
	application.Get(pattern, observeABS(nil, http.MethodGet, pattern, mediaHandler.ServeHTTP))

	gateway := compatgateway.New(compatgateway.Config{
		IdentitySecret: []byte("gateway-stream-deadline-test"),
		LocalHandlers: map[compatgateway.AppKind]http.Handler{
			compatgateway.KindAudiobookshelf: application,
		},
	})
	server := httptest.NewUnstartedServer(gateway)
	server.Config.WriteTimeout = writeTimeout
	server.Start()
	t.Cleanup(server.Close)
	return server
}

// pacedGatewayTrackReader takes slicePace to deliver each production-sized
// ReadFrom slice, independent of the buffer size chosen by net/http. It waits
// on timers derived from cumulative bytes delivered, so scheduler jitter does
// not accumulate and there is no fixed sleep detached from transfer progress.
type pacedGatewayTrackReader struct {
	remaining int64
	delivered int64
	started   time.Time
	slicePace time.Duration
}

func (r *pacedGatewayTrackReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if r.started.IsZero() {
		r.started = time.Now()
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	r.delivered += n
	target := r.started.Add(time.Duration(r.delivered) * r.slicePace / time.Duration(httpstream.ReadFromChunkDefault))
	if wait := time.Until(target); wait > 0 {
		timer := time.NewTimer(wait)
		<-timer.C
	}
	r.remaining -= n
	return int(n), nil
}
