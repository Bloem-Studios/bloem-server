package planstore

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type runtimeTestClock struct{ tick atomic.Int64 }

func (c *runtimeTestClock) Now() (time.Duration, error) { return time.Duration(c.tick.Load()), nil }

func TestExecutorRuntimePostgresRedisRevocation(t *testing.T) {
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
	recipes := noderecipe.NewStore(client, time.Minute)
	f := newGrantFixture(t)
	f.stage(t)
	clock := new(runtimeTestClock)
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: time.Second, SafetyMargin: 100 * time.Millisecond, RenewBefore: 200 * time.Millisecond, PollInterval: time.Millisecond}
	runtime, err := NewExecutorRuntime(f.store, recipes, 1, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	card := playback.RecipeCard{SessionID: f.record.SessionID, TranscodeTransportID: f.route.TransportID, Executor: &f.route.Executor}
	locator, err := recipes.PutImmutable(t.Context(), card)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recipes.DeleteImmutable(context.Background(), locator) })
	if err := f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, nil, locator); err != nil {
		t.Fatal(err)
	}
	resolved, err := runtime.Resolve(t.Context(), f.route.TransportID, f.route.Executor)
	if err != nil || resolved == nil || resolved.SessionID != card.SessionID {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	wrong := f.route.Executor
	wrong.ExecutorID = uuid.NewString()
	if _, err := runtime.Resolve(t.Context(), f.route.TransportID, wrong); err == nil {
		t.Fatal("wrong generation resolved")
	}
	if _, err := runtime.Acquire(t.Context(), f.route.TransportID, f.route.Executor, playback.AttemptGrantServeV3); err == nil {
		t.Fatal("preparing route served")
	}
	grant, err := runtime.Acquire(t.Context(), f.route.TransportID, f.route.Executor, playback.AttemptGrantExecuteV3)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	if err := grant.Check(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.PublishAttempt(t.Context(), f.authority, f.record); err != nil {
		t.Fatal(err)
	}
	// This runtime's configured node is the execution node, not the egress node.
	if _, err := runtime.Acquire(t.Context(), f.route.TransportID, f.route.Executor, playback.AttemptGrantServeV3); err == nil {
		t.Fatal("wrong node served")
	}
	egress, err := NewExecutorRuntime(f.store, recipes, 2, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	serve, err := egress.Acquire(t.Context(), f.route.TransportID, f.route.Executor, playback.AttemptGrantServeV3)
	if err != nil {
		t.Fatal(err)
	}
	defer serve.Close()
	if _, err := f.store.BeginAttemptDrain(t.Context(), f.authority); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Resolve(t.Context(), f.route.TransportID, f.route.Executor); err == nil {
		t.Fatal("draining recipe resolved")
	}
	if _, err := runtime.Acquire(t.Context(), f.route.TransportID, f.route.Executor, playback.AttemptGrantExecuteV3); err == nil {
		t.Fatal("draining grant issued")
	}
	// Advance elapsed time to renewal while the original grants are still valid.
	// The real database rejects renewal after drain, so both supervisors close.
	clock.tick.Store(int64(750 * time.Millisecond))
	timeout, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for _, g := range []*playback.RuntimeGrantV3{grant, serve} {
		select {
		case <-g.Context().Done():
		case <-timeout.Done():
			t.Fatal("draining grant did not close on renewal")
		}
		if err := g.Check(); err == nil {
			t.Fatal("revoked grant revived")
		}
	}
}
