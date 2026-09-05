package transcodenode

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

type workerGrantClock struct{ value atomic.Int64 }

func (c *workerGrantClock) Now() (time.Duration, error) { return time.Duration(c.value.Load()), nil }

func workerTestGrant(t *testing.T, ctx context.Context, clock playback.RuntimeGrantClockV3, ns playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3, transport string, source playback.RuntimeGrantSourceV3) (*playback.RuntimeGrantV3, error) {
	t.Helper()
	a := playback.AttemptAuthorityV3{PlaybackAttemptID: "worker-attempt", Incarnation: ns.Incarnation, Epoch: ns.Epoch, OwnerID: uuid.NewString(), State: playback.AttemptActiveV3, LeaseExpiresAt: time.Now().Add(time.Hour)}
	req := playback.AttemptGrantRequestV3{Executor: ns, SessionID: "session", PlanID: "plan", TransportID: transport, NodeID: 1, Purpose: purpose, Duration: 10 * time.Second}
	if source == nil {
		source = func(_ context.Context, a playback.AttemptAuthorityV3, r playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
			now := time.Now()
			return playback.AttemptGrantV3{Authority: a, Request: r, IssuedAt: now, NotAfter: now.Add(r.Duration)}, nil
		}
	}
	return playback.AcquireRuntimeGrantV3(ctx, source, clock, playback.RuntimeGrantPolicyV3{MaxDuration: 10 * time.Second, SafetyMargin: time.Second, RenewBefore: 2 * time.Second, PollInterval: 100 * time.Millisecond}, a, req)
}

func workerGrantProvider(t *testing.T) playback.ExecutorGrantProviderV3 {
	t.Helper()
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, transport string, ns playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		return workerTestGrant(t, ctx, clock, ns, purpose, transport, nil)
	}
}

type workerDeadlineWriter struct {
	*httptest.ResponseRecorder
	mu       sync.Mutex
	deadline time.Time
	blocked  chan struct{}
	released chan struct{}
	release  sync.Once
}

func (w *workerDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	w.deadline = deadline
	w.mu.Unlock()
	if !deadline.IsZero() && !deadline.After(time.Now()) && w.released != nil {
		w.release.Do(func() { close(w.released) })
	}
	return nil
}
func (w *workerDeadlineWriter) FlushError() error { return nil }
func (w *workerDeadlineWriter) Write(body []byte) (int, error) {
	if w.blocked != nil {
		close(w.blocked)
		<-w.released
		return 0, os.ErrDeadlineExceeded
	}
	return w.ResponseRecorder.Write(body)
}

func TestExecutorGrantWriterExpiryAndDeadlineCap(t *testing.T) {
	clock := &workerGrantClock{}
	grant, err := workerTestGrant(t, t.Context(), clock, *workerNamespace(), playback.AttemptGrantServeV3, "transport", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := &workerDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	writer, cleanup, err := guardWorkerTestResponse(base, t.Context(), grant)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	base.mu.Lock()
	deadline := base.deadline
	base.mu.Unlock()
	if time.Until(deadline) > 9*time.Second {
		t.Fatalf("rolling deadline escaped grant: %v", deadline)
	}
	clock.value.Store(int64(10 * time.Second))
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write([]byte("forbidden")); err == nil {
		t.Fatal("expired grant wrote response")
	}
	if base.Body.Len() != 0 || base.Flushed {
		t.Fatal("expired grant emitted bytes")
	}
}

func TestExecutorGrantWriterRequiresDeadlineSupport(t *testing.T) {
	grant, err := workerTestGrant(t, t.Context(), &workerGrantClock{}, *workerNamespace(), playback.AttemptGrantServeV3, "transport", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	if _, _, err := guardWorkerTestResponse(httptest.NewRecorder(), t.Context(), grant); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("unsupported writer accepted: %v", err)
	}
}

func TestExecutorGrantWriterInterruptsBlockedWrite(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		name := "grant revoked"
		if disconnect {
			name = "client disconnect"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			grant, err := workerTestGrant(t, ctx, &workerGrantClock{}, *workerNamespace(), playback.AttemptGrantServeV3, "transport", nil)
			if err != nil {
				t.Fatal(err)
			}
			base := &workerDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), blocked: make(chan struct{}), released: make(chan struct{})}
			writer, cleanup, err := guardWorkerTestResponse(base, ctx, grant)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			done := make(chan error, 1)
			go func() { _, err := writer.Write([]byte("blocked")); done <- err }()
			select {
			case <-base.blocked:
			case <-time.After(2 * time.Second):
				t.Fatal("write did not enter transport")
			}
			if disconnect {
				cancel()
			} else {
				grant.Close()
			}
			select {
			case err := <-done:
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("blocked write: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("blocked write survived authority cancellation")
			}
			if _, err := writer.Write([]byte("later")); err == nil {
				t.Fatal("response resumed after cancellation")
			}
		})
	}
}

func TestExecutorResponseRejectsLateOrWrongGrant(t *testing.T) {
	s := newTestServer(t)
	ns := workerNamespace()
	session := workerBoundSession(t, s, ns)
	for _, wrong := range []bool{false, true} {
		name := "late"
		if wrong {
			name = "wrong binding"
		}
		t.Run(name, func(t *testing.T) {
			clock := &workerGrantClock{}
			s.WithExecutorGrantProvider(func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
				if wrong {
					executor = *workerNamespace()
				}
				source := func(_ context.Context, a playback.AttemptAuthorityV3, r playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
					now := time.Now()
					if !wrong {
						clock.value.Store(int64(10 * time.Second))
					}
					return playback.AttemptGrantV3{Authority: a, Request: r, IssuedAt: now, NotAfter: now.Add(r.Duration)}, nil
				}
				return workerTestGrant(t, ctx, clock, executor, purpose, transport, source)
			})
			w := &workerDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
			if _, _, _, err := s.grantExecutorResponse(w, httptest.NewRequest(http.MethodGet, "/", nil), "worker-session", session); err == nil {
				t.Fatal("invalid serving grant accepted")
			}
			if w.Body.Len() != 0 {
				t.Fatal("grant failure emitted body")
			}
		})
	}
}

func TestExecutorGrantRealHTTPDoesNotResumeAfterRevocation(t *testing.T) {
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := workerTestGrant(t, t.Context(), clock, *workerNamespace(), playback.AttemptGrantServeV3, "transport", nil)
	if err != nil {
		t.Fatal(err)
	}
	denied := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer, cleanup, err := guardWorkerTestResponse(w, r.Context(), grant)
		if err != nil {
			denied <- err
			return
		}
		defer cleanup()
		if _, err := writer.Write([]byte("allowed")); err != nil {
			denied <- err
			return
		}
		grant.Close()
		_, err = writer.Write([]byte("forbidden"))
		denied <- err
	}))
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatal(readErr)
	}
	if string(body) != "allowed" {
		t.Fatalf("wire body=%q", body)
	}
	select {
	case err := <-denied:
		if err == nil {
			t.Fatal("revoked HTTP response resumed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP handler did not finish")
	}
}

// The small socket buffers and unread 16 MiB response force the real net/http
// write path to depend on its ResponseController deadline, not a fake writer.
func TestExecutorGrantInterruptsUnreadSocketResponse(t *testing.T) {
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	grant, err := workerTestGrant(t, ctx, clock, *workerNamespace(), playback.AttemptGrantServeV3, "transport", nil)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed := &workerObservedSocketWriter{ResponseWriter: w, started: started}
		writer, cleanup, err := guardWorkerTestResponse(observed, r.Context(), grant)
		if err != nil {
			done <- err
			return
		}
		defer cleanup()
		if _, err := writer.Write([]byte("ready")); err != nil {
			done <- err
			return
		}
		_, err = writer.Write(make([]byte, 16<<20))
		done <- err
	}))
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateNew {
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.SetWriteBuffer(1024)
			}
		}
	}
	server.Start()
	defer server.Close()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.SetReadBuffer(1024); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	// Stop reading the response body before the large transport write begins.
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("large socket write never began")
	}
	select {
	case err := <-done:
		t.Fatalf("unread oversized response unexpectedly completed: %v", err)
	default:
	}
	grant.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revocation allowed entire unread response")
		}
	case <-ctx.Done():
		t.Fatal("revoked socket write remained blocked")
	}
}

type workerObservedSocketWriter struct {
	http.ResponseWriter
	started chan struct{}
}

func (w *workerObservedSocketWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *workerObservedSocketWriter) Write(body []byte) (int, error) {
	if len(body) > 1<<20 {
		close(w.started)
	}
	return w.ResponseWriter.Write(body)
}

// Exercise the shared response guard with an already acquired test lease.
func guardWorkerTestResponse(w http.ResponseWriter, ctx context.Context, grant *playback.RuntimeGrantV3) (http.ResponseWriter, func(), error) {
	request := grant.Request()
	provider := func(context.Context, string, playback.ExecutorNamespaceV3, playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		return grant, nil
	}
	writer, _, cleanup, err := playback.GuardExecutorResponseV3(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx), provider, request.TransportID, &request.Executor)
	return writer, cleanup, err
}
