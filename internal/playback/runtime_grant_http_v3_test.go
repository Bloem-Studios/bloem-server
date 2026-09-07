package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type runtimeGrantHTTPRecorder struct {
	*httptest.ResponseRecorder
}

func TestGuardOutputTransferRequiresExecutor(t *testing.T) {
	base := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w, _, cleanup, err := GuardExecutorOutputV3(base, r, nil, "transport", nil, AttemptGrantTransferV3)
	if err == nil || w != nil || cleanup != nil || base.Body.Len() != 0 {
		t.Fatalf("unbound output transfer passed through: writer=%T cleanup=%v err=%v", w, cleanup != nil, err)
	}
	w, _, cleanup, err = GuardExecutorResponseV3(base, r, nil, "legacy", nil)
	if err != nil || w != base || cleanup == nil {
		t.Fatalf("legacy serving changed: writer=%T cleanup=%v err=%v", w, cleanup != nil, err)
	}
	cleanup()
}

func TestGuardExecutorResponseV3ClosesGrantOnProviderError(t *testing.T) {
	a, bound, policy := runtimeGrantFixture()
	grant, err := AcquireRuntimeGrantV3(t.Context(), func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
		return runtimeGrantReply(a, r), nil
	}, &runtimeGrantTestClock{}, policy, a, bound)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(grant.Close)
	providerErr := errors.New("provider failed after acquisition")
	provider := func(context.Context, string, ExecutorNamespaceV3, AttemptGrantPurposeV3) (*RuntimeGrantV3, error) {
		return grant, providerErr
	}
	w, _, cleanup, err := GuardExecutorResponseV3(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), provider, bound.TransportID, &bound.Executor)
	if !errors.Is(err, providerErr) || w != nil || cleanup != nil {
		t.Fatalf("failed provider response: writer=%T cleanup=%v err=%v", w, cleanup != nil, err)
	}
	select {
	case <-grant.Context().Done():
	default:
		t.Fatal("provider error leaked live grant")
	}
	if grant.Check() == nil {
		t.Fatal("provider error left grant usable")
	}
}

func TestGuardExecutorResponseV3HTTP2Cancellation(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnect=%v", disconnect), func(t *testing.T) {
			a, bound, policy := runtimeGrantFixture()
			grant, err := AcquireRuntimeGrantV3(t.Context(), func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
				return runtimeGrantReply(a, r), nil
			}, &runtimeGrantTestClock{}, policy, a, bound)
			if err != nil {
				t.Fatal(err)
			}
			provider := func(context.Context, string, ExecutorNamespaceV3, AttemptGrantPurposeV3) (*RuntimeGrantV3, error) {
				return grant, nil
			}
			denied := make(chan error, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 {
					denied <- fmt.Errorf("server protocol=%s", r.Proto)
					return
				}
				writer, guarded, cleanup, err := GuardExecutorResponseV3(w, r, provider, bound.TransportID, &bound.Executor)
				if err != nil {
					denied <- err
					return
				}
				defer cleanup()
				if _, err := writer.Write([]byte("allowed")); err != nil {
					denied <- err
					return
				}
				<-guarded.Context().Done()
				_, err = writer.Write([]byte("forbidden"))
				denied <- err
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			t.Cleanup(server.Close)
			t.Cleanup(grant.Close)
			client := server.Client()
			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatal("unexpected test client transport")
			}
			transport.ForceAttemptHTTP2 = true
			client.Timeout = 3 * time.Second
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := response.Body.Close(); err != nil {
					t.Error(err)
				}
			}()
			if response.ProtoMajor != 2 {
				t.Fatalf("client protocol=%s", response.Proto)
			}
			prefix := make([]byte, len("allowed"))
			if _, err := io.ReadFull(response.Body, prefix); err != nil || string(prefix) != "allowed" {
				t.Fatalf("initial HTTP/2 bytes=%q err=%v", prefix, err)
			}
			if disconnect {
				cancel()
			} else {
				grant.Close()
			}
			select {
			case err := <-denied:
				if err == nil {
					t.Fatal("HTTP/2 continuation accepted after cancellation")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP/2 cancellation did not release handler")
			}
			remaining, _ := io.ReadAll(response.Body)
			if len(remaining) != 0 {
				t.Fatalf("HTTP/2 emitted bytes after cancellation: %q", remaining)
			}
			if grant.Check() == nil {
				t.Fatal("HTTP/2 cancellation left grant live")
			}
		})
	}
}

func (*runtimeGrantHTTPRecorder) SetWriteDeadline(time.Time) error { return nil }

func TestGuardExecutorResponseV3LegacyAndMissingProvider(t *testing.T) {
	base := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	w, r, cleanup, err := GuardExecutorResponseV3(base, request, nil, "legacy", nil)
	if err != nil || w != base || r != request || cleanup == nil {
		t.Fatalf("legacy passthrough: writer=%T request=%p cleanup=%v err=%v", w, r, cleanup != nil, err)
	}
	cleanup()
	_, bound, _ := runtimeGrantFixture()
	if _, _, _, err := GuardExecutorResponseV3(base, request, nil, bound.TransportID, &bound.Executor); err == nil {
		t.Fatal("bound response accepted without provider")
	}
}

func TestGuardExecutorResponseV3OwnsGrantWithoutWriteBypass(t *testing.T) {
	a, bound, policy := runtimeGrantFixture()
	clock := &runtimeGrantTestClock{}
	var grant *RuntimeGrantV3
	provider := func(ctx context.Context, transport string, executor ExecutorNamespaceV3, purpose AttemptGrantPurposeV3) (*RuntimeGrantV3, error) {
		if transport != bound.TransportID || executor != bound.Executor || purpose != AttemptGrantServeV3 {
			t.Fatal("provider received wrong binding")
		}
		var err error
		grant, err = AcquireRuntimeGrantV3(ctx, func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
			return runtimeGrantReply(a, r), nil
		}, clock, policy, a, bound)
		return grant, err
	}
	base := &runtimeGrantHTTPRecorder{ResponseRecorder: httptest.NewRecorder()}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	w, r, cleanup, err := GuardExecutorResponseV3(base, request, provider, bound.TransportID, &bound.Executor)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if r.Context() != grant.Context() {
		t.Fatal("response did not inherit grant cancellation")
	}
	if _, ok := w.(io.ReaderFrom); ok {
		t.Fatal("ReaderFrom bypass exposed")
	}
	if _, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
		t.Fatal("raw writer bypass exposed")
	}
	if _, err := w.Write([]byte("live")); err != nil {
		t.Fatal(err)
	}
	clock.set(10*time.Second, nil)
	if _, err := w.Write([]byte("expired")); !errors.Is(err, ErrRuntimeGrantExpiredV3) {
		t.Fatalf("expired response write: %v", err)
	}
	if base.Body.String() != "live" {
		t.Fatalf("unexpected response bytes %q", base.Body.String())
	}
}
