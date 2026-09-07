package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/playback/planstore"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// initialNodeRuntime owns only this node's grant supervisors. It does not enroll
// sources or activate routes. Its configured ID is never taken from requests.
type initialNodeRuntime struct {
	runtime *planstore.ExecutorRuntime
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	work    sync.WaitGroup
	done    chan struct{}
}

func configureInitialNodePlayback(ctx context.Context, bootstrap *config.BootstrapConfig, pool *pgxpool.Pool, redisClient *redis.Client, nodeID int, signingKey string) (*initialNodeRuntime, error) {
	if bootstrap == nil {
		return nil, errors.New("bootstrap configuration required")
	}
	if !bootstrap.InitialPlaybackEnabled {
		return nil, nil
	}
	if ctx == nil || (bootstrap.Mode != "proxy" && bootstrap.Mode != "transcode") || pool == nil || redisClient == nil || nodeID <= 0 || signingKey == "" {
		return nil, errors.New("configured worker node, PostgreSQL, Redis, signing key and application context required")
	}
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(startup); err != nil {
		return nil, fmt.Errorf("initial node PostgreSQL unavailable: %w", err)
	}
	if err := redisClient.Ping(startup).Err(); err != nil {
		return nil, fmt.Errorf("initial node Redis unavailable: %w", err)
	}
	_, policy := initialPlaybackTestingPolicies()
	store, err := planstore.NewPostgresWithGrantPolicy(pool, playback.AttemptGrantPolicyV3{MaxDuration: policy.MaxDuration})
	if err != nil {
		return nil, err
	}
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		return nil, err
	}
	runtime, err := planstore.NewExecutorRuntime(store, noderecipe.NewStore(redisClient, playback.MaxTokenTTL), nodeID, clock, policy)
	if err != nil {
		return nil, err
	}
	return newInitialNodeRuntime(ctx, runtime), nil
}

func newInitialNodeRuntime(ctx context.Context, runtime *planstore.ExecutorRuntime) *initialNodeRuntime {
	lifetime, cancelLifetime := context.WithCancel(ctx)
	node := &initialNodeRuntime{runtime: runtime, ctx: lifetime, cancel: cancelLifetime, done: make(chan struct{})}
	go func() {
		<-lifetime.Done()
		node.mu.Lock()
		node.closed = true
		node.mu.Unlock()
		node.work.Wait()
		close(node.done)
	}()
	return node
}

func (n *initialNodeRuntime) acquire(ctx context.Context, acquire func(context.Context) (*playback.RuntimeGrantV3, error)) (*playback.RuntimeGrantV3, error) {
	n.mu.Lock()
	if n.closed || n.ctx.Err() != nil {
		n.mu.Unlock()
		return nil, errors.New("initial node playback is shutting down")
	}
	n.work.Add(1)
	n.mu.Unlock()
	defer n.work.Done()
	lifetime, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(n.ctx, cancel)
	grant, err := acquire(lifetime)
	if err != nil || grant == nil {
		stop()
		cancel()
		if grant != nil {
			grant.Close()
		}
		if err == nil {
			err = errors.New("initial node grant missing")
		}
		return nil, err
	}
	n.work.Go(func() { defer stop(); defer cancel(); <-grant.Done() })
	return grant, nil
}

func (n *initialNodeRuntime) Acquire(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
	return n.acquire(ctx, func(ctx context.Context) (*playback.RuntimeGrantV3, error) {
		return n.runtime.Acquire(ctx, transport, executor, purpose)
	})
}
func (n *initialNodeRuntime) AcquireTransfer(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, permit string) (*playback.RuntimeGrantV3, error) {
	return n.acquire(ctx, func(ctx context.Context) (*playback.RuntimeGrantV3, error) {
		return n.runtime.AcquireOutputTransfer(ctx, transport, executor, permit)
	})
}
func (n *initialNodeRuntime) Shutdown(ctx context.Context) error {
	n.cancel()
	select {
	case <-n.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (n *initialNodeRuntime) OpenTransfer(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3) (string, func(), error) {
	n.mu.Lock()
	if n.closed || n.ctx.Err() != nil {
		n.mu.Unlock()
		return "", nil, errors.New("initial node playback is shutting down")
	}
	n.work.Add(1)
	n.mu.Unlock()
	permit, closePermit, err := n.runtime.OpenOutputTransfer(ctx, transport, executor)
	if err != nil || permit == "" || closePermit == nil {
		if closePermit != nil {
			closePermit()
		}
		n.work.Done()
		if err == nil {
			err = errors.New("initial node output transfer permit missing")
		}
		return "", nil, err
	}
	return permit, sync.OnceFunc(func() { defer n.work.Done(); closePermit() }), nil
}
