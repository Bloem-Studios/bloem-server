package abs

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/compatgateway"
	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/Silo-Server/silo-server/internal/models"
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

// TestMountedABSHandlerFeedDeadlineSurvivesPublicWriteTimeout exercises the
// production Handler.Mount registration and a real http.ServeFile route. The
// file takes longer than the public server's absolute WriteTimeout to consume,
// while each production-sized ReadFrom slice completes within the rolling
// stall window.
func TestMountedABSHandlerFeedDeadlineSurvivesPublicWriteTimeout(t *testing.T) {
	t.Setenv("SILO_STREAM_WRITE_STALL_TIMEOUT", "1")

	const total = 2 * httpstream.ReadFromChunkDefault
	path := t.TempDir() + "/feed.mp3"
	if err := os.WriteFile(path, make([]byte, total), 0o644); err != nil {
		t.Fatalf("write feed fixture: %v", err)
	}

	h := New(Dependencies{
		MediaStore: &feedMediaStore{file: &models.MediaFile{
			ID: 501, FilePath: path, ContentID: "book-9",
		}},
		RSSFeedStore: &feedStore{feed: RSSFeed{
			ID: "feed-1", UserID: "7", ProfileID: "profile-9", LibraryItemID: "book-9", Slug: "slug-9",
		}},
	})
	application := chi.NewRouter()
	h.Mount(application)
	gateway := compatgateway.New(compatgateway.Config{
		IdentitySecret: []byte("gateway-mounted-handler-deadline-test"),
		LocalHandlers: map[compatgateway.AppKind]http.Handler{
			compatgateway.KindAudiobookshelf: application,
		},
	})
	server := httptest.NewUnstartedServer(gateway)
	server.Config.WriteTimeout = gatewayTestWriteTimeout
	server.Listener = &gatewayWriteBufferListener{Listener: server.Listener, bytes: 16 << 10}
	server.Start()
	t.Cleanup(server.Close)

	address := strings.TrimPrefix(server.URL, "http://")
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatalf("dial mounted ABS server: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * gatewayTestStallWindow)); err != nil {
		t.Fatalf("set client deadline: %v", err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.SetReadBuffer(16 << 10); err != nil {
			t.Fatalf("set client read buffer: %v", err)
		}
	}
	req, err := http.NewRequest(http.MethodGet, server.URL+"/audiobookshelf/feed/slug-9/file/501", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if err := req.Write(conn); err != nil {
		t.Fatalf("write request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("response status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	started := time.Now()
	bytesRead, err := io.Copy(io.Discard, &pacedGatewayResponseReader{
		source: response.Body, slicePace: 600 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("production mounted feed died before completing %d bytes: read %d: %v", total, bytesRead, err)
	}
	if bytesRead != total {
		t.Fatalf("production mounted feed body = %d bytes, want %d", bytesRead, total)
	}
	if elapsed := time.Since(started); elapsed <= gatewayTestWriteTimeout {
		t.Fatalf("feed completed in %s, test did not outlive public WriteTimeout %s", elapsed, gatewayTestWriteTimeout)
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

type gatewayWriteBufferListener struct {
	net.Listener
	bytes int
}

func (l *gatewayWriteBufferListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.SetWriteBuffer(l.bytes); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

type pacedGatewayResponseReader struct {
	source    io.Reader
	delivered int64
	started   time.Time
	slicePace time.Duration
}

func (r *pacedGatewayResponseReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if n == 0 {
		return n, err
	}
	if r.started.IsZero() {
		r.started = time.Now()
	}
	r.delivered += int64(n)
	target := r.started.Add(time.Duration(r.delivered) * r.slicePace / time.Duration(httpstream.ReadFromChunkDefault))
	if wait := time.Until(target); wait > 0 {
		timer := time.NewTimer(wait)
		<-timer.C
	}
	return n, err
}
