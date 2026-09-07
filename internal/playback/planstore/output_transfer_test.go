package planstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func outputTransferRuntimes(t *testing.T, f *grantFixture) (*ExecutorRuntime, *ExecutorRuntime) {
	t.Helper()
	raw := os.Getenv("SILO_TEST_REDIS_URL")
	if raw == "" {
		t.Skip("real Redis requires SILO_TEST_REDIS_URL")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	recipes := noderecipe.NewStore(client, time.Minute)
	card := playback.RecipeCard{SessionID: f.record.SessionID, TranscodeTransportID: f.route.TransportID, Executor: &f.route.Executor,
		RoutingExecutionNodeID: f.route.ExecutionNodeID, RoutingEgressNodeID: f.route.EgressNodeID}
	locator, err := recipes.PutImmutable(t.Context(), card)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recipes.DeleteImmutable(context.Background(), locator) })
	if err := f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, nil, locator); err != nil {
		t.Fatal(err)
	}
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: time.Second, SafetyMargin: 100 * time.Millisecond, RenewBefore: 200 * time.Millisecond, PollInterval: 10 * time.Millisecond}
	worker, err := NewExecutorRuntime(f.store, recipes, f.route.ExecutionNodeID, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	egress, err := NewExecutorRuntime(f.store, recipes, f.route.EgressNodeID, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	return worker, egress
}

func TestOutputTransferRequiresSelectedEgressAndExactBinding(t *testing.T) {
	t.Run("api", func(t *testing.T) { testOutputTransferBinding(t, 0) })
	t.Run("proxy", func(t *testing.T) { testOutputTransferBinding(t, 2) })
}

func testOutputTransferBinding(t *testing.T, egressNodeID int) {
	t.Helper()
	f := newGrantFixture(t)
	f.route.EgressNodeID = egressNodeID
	f.stage(t)
	worker, egress := outputTransferRuntimes(t, f)
	if _, _, err := egress.OpenOutputTransfer(t.Context(), f.route.TransportID, f.route.Executor); err == nil {
		t.Fatal("preparing egress opened transfer")
	}
	if err := f.store.PublishAttempt(t.Context(), f.authority, f.record); err != nil {
		t.Fatal(err)
	}
	if _, _, err := worker.OpenOutputTransfer(t.Context(), f.route.TransportID, f.route.Executor); err == nil {
		t.Fatal("execution node impersonated egress")
	}
	if _, err := worker.Acquire(t.Context(), f.route.TransportID, f.route.Executor, playback.AttemptGrantServeV3); err == nil {
		t.Fatal("transfer widened serve authority")
	}
	permit, closePermit, err := egress.OpenOutputTransfer(t.Context(), f.route.TransportID, f.route.Executor)
	if err != nil {
		t.Fatal(err)
	}
	defer closePermit()
	if _, err := egress.AcquireOutputTransfer(t.Context(), f.route.TransportID, f.route.Executor, permit); err == nil {
		t.Fatal("egress impersonated execution node")
	}
	for _, id := range []string{"", "invalid", uuid.NewString()} {
		if _, err := worker.AcquireOutputTransfer(t.Context(), f.route.TransportID, f.route.Executor, id); err == nil {
			t.Fatal("unknown permit granted transfer")
		}
	}
	grant, err := worker.AcquireOutputTransfer(t.Context(), f.route.TransportID, f.route.Executor, permit)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	request := grant.Request()
	if request.NodeID != f.route.ExecutionNodeID || request.EgressNodeID != f.route.EgressNodeID || request.OutputTransferID != permit {
		t.Fatalf("transfer lost hop identity: %+v", request)
	}
	for name, mutate := range map[string]func(*playback.AttemptGrantRequestV3){
		"peer":        func(r *playback.AttemptGrantRequestV3) { r.EgressNodeID++ },
		"execution":   func(r *playback.AttemptGrantRequestV3) { r.NodeID++ },
		"session":     func(r *playback.AttemptGrantRequestV3) { r.SessionID = uuid.NewString() },
		"plan":        func(r *playback.AttemptGrantRequestV3) { r.PlanID += "wrong" },
		"transport":   func(r *playback.AttemptGrantRequestV3) { r.TransportID = uuid.NewString() },
		"executor":    func(r *playback.AttemptGrantRequestV3) { r.Executor.ExecutorID = uuid.NewString() },
		"epoch":       func(r *playback.AttemptGrantRequestV3) { r.Executor.Epoch++ },
		"incarnation": func(r *playback.AttemptGrantRequestV3) { r.Executor.Incarnation = uuid.NewString() },
		"purpose":     func(r *playback.AttemptGrantRequestV3) { r.Purpose = playback.AttemptGrantServeV3 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			mutate(&changed)
			_, err := f.store.IssueAttemptGrant(t.Context(), f.authority, changed)
			requireStaleGrant(t, err)
		})
	}
	// A permit from another attempt cannot substitute even if its selected nodes
	// have the same identities.
	other := newGrantFixture(t)
	other.stage(t)
	_, otherEgress := outputTransferRuntimes(t, other)
	if err := other.store.PublishAttempt(t.Context(), other.authority, other.record); err != nil {
		t.Fatal(err)
	}
	otherPermit, closeOther, err := otherEgress.OpenOutputTransfer(t.Context(), other.route.TransportID, other.route.Executor)
	if err != nil {
		t.Fatal(err)
	}
	defer closeOther()
	if _, err := worker.AcquireOutputTransfer(t.Context(), f.route.TransportID, f.route.Executor, otherPermit); err == nil {
		t.Fatal("cross-attempt permit accepted")
	}
	closePermit()
	if _, err := worker.AcquireOutputTransfer(t.Context(), f.route.TransportID, f.route.Executor, permit); err == nil {
		t.Fatal("closed permit issued another grant")
	}
}

func TestOutputTransferRealHTTPBothHopsDrain(t *testing.T) {
	f := newGrantFixture(t)
	f.stage(t)
	worker, egress := outputTransferRuntimes(t, f)
	if err := f.store.PublishAttempt(t.Context(), f.authority, f.record); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	egressFinished := make(chan error, 1)
	workerHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider := func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
			return worker.AcquireOutputTransfer(ctx, transport, executor, r.Header.Get(playback.OutputTransferHeaderV3))
		}
		writer, request, closeGrant, err := playback.GuardExecutorOutputV3(w, r, provider, f.route.TransportID, &f.route.Executor, playback.AttemptGrantTransferV3)
		if err != nil {
			http.Error(w, "denied", http.StatusServiceUnavailable)
			return
		}
		defer closeGrant()
		if _, err := writer.Write([]byte("allowed")); err != nil {
			finished <- err
			return
		}
		<-request.Context().Done()
		_, err = writer.Write([]byte("forbidden"))
		finished <- err
	}))
	defer workerHTTP.Close()
	egressHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer, request, closeGrant, err := playback.GuardExecutorResponseV3(w, r, egress.Acquire, f.route.TransportID, &f.route.Executor)
		if err != nil {
			http.Error(w, "denied", http.StatusServiceUnavailable)
			return
		}
		defer closeGrant()
		permit, closePermit, err := egress.OpenOutputTransfer(request.Context(), f.route.TransportID, f.route.Executor)
		if err != nil {
			return
		}
		defer closePermit()
		upstream, err := http.NewRequestWithContext(request.Context(), http.MethodGet, workerHTTP.URL, nil)
		if err != nil {
			return
		}
		upstream.Header.Set(playback.OutputTransferHeaderV3, permit)
		response, err := workerHTTP.Client().Do(upstream)
		if err != nil {
			return
		}
		defer response.Body.Close()
		_, _ = io.Copy(writer, response.Body)
		<-request.Context().Done()
		_, err = writer.Write([]byte("egress-forbidden"))
		egressFinished <- err
	}))
	defer egressHTTP.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, egressHTTP.URL, nil)
	response, err := egressHTTP.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, len("allowed"))
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "allowed" {
		t.Fatalf("first bytes %q: %v", first, err)
	}
	// Record an issued transfer deadline as if its response was lost. Drain must
	// retain it alongside both independently issued HTTP grants.
	permit, closePermit, err := egress.OpenOutputTransfer(ctx, f.route.TransportID, f.route.Executor)
	if err != nil {
		t.Fatal(err)
	}
	defer closePermit()
	grant, err := worker.AcquireOutputTransfer(ctx, f.route.TransportID, f.route.Executor, permit)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	receipt, err := f.store.IssueAttemptGrant(ctx, f.authority, grant.Request())
	if err != nil {
		t.Fatal(err)
	}
	drain, err := f.store.BeginAttemptDrain(ctx, f.authority)
	if err != nil || drain.NotBefore.Before(receipt.NotAfter) {
		t.Fatalf("drain omitted transfer: %+v %v", drain, err)
	}
	if _, _, err := egress.OpenOutputTransfer(ctx, f.route.TransportID, f.route.Executor); err == nil {
		t.Fatal("draining egress opened permit")
	}
	if _, err := worker.AcquireOutputTransfer(ctx, f.route.TransportID, f.route.Executor, permit); err == nil {
		t.Fatal("draining worker acquired transfer")
	}
	rest, _ := io.ReadAll(response.Body)
	if strings.Contains(string(rest), "forbidden") {
		t.Fatalf("expired bytes reached client: %q", rest)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("worker resumed after drain")
		}
	case <-ctx.Done():
		t.Fatal("worker did not cancel after drain")
	}
	select {
	case err := <-egressFinished:
		if err == nil {
			t.Fatal("egress resumed after drain")
		}
	case <-ctx.Done():
		t.Fatal("egress did not cancel after drain")
	}
	if time.Now().After(drain.NotBefore) {
		t.Fatal("HTTP hops outlived the persisted drain deadline")
	}
}
