package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/google/uuid"
)

func proxyExecutorFixture(t *testing.T, card *playback.RecipeCard) *Server {
	t.Helper()
	s := newSocketProxyServer(t, "executor-test", clientip.NewResolver(nil))
	s.nodeRowID = func() (int, bool) { return 2, true }
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	s.WithExecutorRuntime(func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		if purpose != playback.AttemptGrantServeV3 || transport != card.TranscodeTransportID || executor != *card.Executor {
			return nil, errors.New("wrong final serving grant")
		}
		a := playback.AttemptAuthorityV3{PlaybackAttemptID: "attempt", Incarnation: executor.Incarnation, Epoch: executor.Epoch, OwnerID: uuid.NewString(), State: playback.AttemptActiveV3, LeaseExpiresAt: time.Now().Add(time.Minute)}
		request := playback.AttemptGrantRequestV3{Executor: executor, SessionID: card.SessionID, PlanID: "plan", TransportID: transport, NodeID: 2, Purpose: purpose, Duration: 10 * time.Second}
		return playback.AcquireRuntimeGrantV3(ctx, func(_ context.Context, a playback.AttemptAuthorityV3, r playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
			now := time.Now()
			return playback.AttemptGrantV3{Authority: a, Request: r, IssuedAt: now, NotAfter: now.Add(r.Duration)}, nil
		}, clock, playback.RuntimeGrantPolicyV3{MaxDuration: 10 * time.Second, SafetyMargin: time.Second, RenewBefore: 2 * time.Second, PollInterval: 10 * time.Millisecond}, a, request)
	}, func(_ context.Context, transport string, executor playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		if transport != card.TranscodeTransportID || executor != *card.Executor {
			return nil, errors.New("wrong descriptor")
		}
		return card, nil
	}, nil)
	return s
}

func TestProxyExecutorDirectRequiresCurrentRecipeAndEgress(t *testing.T) {
	for _, mode := range []string{"valid", "wrong node", "stale source", "missing runtime", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			card := playback.NewDirectRecipeCard("logical", 7, "profile", 42)
			card.InputPath = writeSocketProxyMedia(t)
			card.Executor = &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
			card.TranscodeTransportID = "transport"
			card.RoutingWorkload, card.RoutingExecution, card.RoutingEgress = string(noderouting.WorkloadDirectPlay), string(noderouting.ExecutionNone), string(noderouting.EgressProxy)
			card.RoutingEgressNodeID = 2
			s := proxyExecutorFixture(t, &card)
			token, err := streamtoken.Sign(card.ToClaims(), "executor-test", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "wrong node":
				s.nodeRowID = func() (int, bool) { return 3, true }
			case "stale source":
				card.InputPath = "/unavailable"
			case "missing runtime":
				s.WithExecutorRuntime(nil, nil, nil)
			case "stopped":
				s.executorGrants = func(context.Context, string, playback.ExecutorNamespaceV3, playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
					return nil, playback.ErrStaleAttemptAuthorityV3
				}
			}
			server := httptest.NewServer(s.Handler())
			defer server.Close()
			response := socketProxyRequest(t, server.Client(), http.MethodGet, server.URL+"/stream/direct/"+token, nil)
			if mode == "valid" {
				if response.status != 200 || response.body != socketProxyMedia {
					t.Fatalf("direct status=%d body=%q", response.status, response.body)
				}
			} else if response.status != 503 || response.body == socketProxyMedia {
				t.Fatalf("invalid authority status=%d", response.status)
			}
		})
	}
}

func TestProxyExecutorTransferRequiresPermitAndRejectsRedirect(t *testing.T) {
	for _, mode := range []string{"valid", "missing permit", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed selected-worker redirect") }))
			defer target.Close()
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get(playback.OutputTransferHeaderV3) != "permit" || r.Header.Get("X-Silo-Stream-Token") != "signed-token" {
					t.Error("missing internal authority")
				}
				if mode == "redirect" {
					http.Redirect(w, r, target.URL, 307)
					return
				}
				w.Header().Set(playback.OutputTransferHeaderV3, "private")
				_, _ = w.Write([]byte("media"))
			}))
			defer worker.Close()
			card := playback.NewDirectRecipeCard("logical", 7, "profile", 42)
			card.PlayMethod = playback.PlayTranscode
			card.Executor = &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
			card.TranscodeTransportID = "transport"
			card.TranscodeNodeURL = worker.URL
			card.RoutingWorkload, card.RoutingExecution, card.RoutingEgress = string(noderouting.WorkloadVideoTranscode), string(noderouting.ExecutionTranscode), string(noderouting.EgressProxy)
			card.RoutingExecutionNodeID = 1
			card.RoutingEgressNodeID = 2
			s := proxyExecutorFixture(t, &card)
			closed := make(chan struct{}, 1)
			if mode != "missing permit" {
				s.executorOutputTransfers = func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3) (string, func(), error) {
					if transport != "transport" || executor != *card.Executor {
						t.Error("wrong transfer binding")
					}
					return "permit", func() { closed <- struct{}{} }, nil
				}
			}
			claims := card.ToClaims()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.proxyToTranscodeNode(w, r, &claims, "/transcode/transport/manifest", "signed-token")
			}))
			defer server.Close()
			response := socketProxyRequest(t, server.Client(), http.MethodGet, server.URL, nil)
			if response.header.Get(playback.OutputTransferHeaderV3) != "" {
				t.Fatal("private permit escaped")
			}
			switch mode {
			case "valid":
				if response.status != 200 || response.body != "media" {
					t.Fatalf("response=%+v", response)
				}
			case "redirect":
				if response.status != 502 || response.header.Get("Location") != "" {
					t.Fatalf("redirect escaped=%+v", response)
				}
			case "missing permit":
				if response.status != 503 || calls.Load() != 0 {
					t.Fatal("missing permit reached worker")
				}
			}
			if mode != "missing permit" {
				if calls.Load() != 1 {
					t.Fatal("worker replayed")
				}
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("permit not closed")
				}
			}
		})
	}
}
