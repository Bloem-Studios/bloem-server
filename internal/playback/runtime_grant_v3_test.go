package playback

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type runtimeGrantTestClock struct {
	mu  sync.Mutex
	now time.Duration
	err error
}

func (c *runtimeGrantTestClock) Now() (time.Duration, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now, c.err
}
func (c *runtimeGrantTestClock) set(now time.Duration, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now, c.err = now, err
}

func runtimeGrantFixture() (AttemptAuthorityV3, AttemptGrantRequestV3, RuntimeGrantPolicyV3) {
	ns := ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
	a := AttemptAuthorityV3{PlaybackAttemptID: uuid.NewString(), OwnerID: uuid.NewString(), Incarnation: ns.Incarnation, Epoch: ns.Epoch, State: AttemptActiveV3}
	r := AttemptGrantRequestV3{Executor: ns, SessionID: uuid.NewString(), PlanID: uuid.NewString(), TransportID: uuid.NewString(), Purpose: AttemptGrantServeV3, NodeID: 7, Duration: 10 * time.Second}
	p := RuntimeGrantPolicyV3{MaxDuration: 10 * time.Second, SafetyMargin: time.Second, RenewBefore: 3 * time.Second, PollInterval: time.Millisecond}
	return a, r, p
}

func runtimeGrantReply(a AttemptAuthorityV3, r AttemptGrantRequestV3) AttemptGrantV3 {
	issued := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	a.LeaseExpiresAt = issued.Add(r.Duration)
	return AttemptGrantV3{Authority: a, Request: r, IssuedAt: issued, NotAfter: issued.Add(r.Duration)}
}

func TestRuntimeGrantRoundTripBudgetAndBinding(t *testing.T) {
	a, r, p := runtimeGrantFixture()
	clock := &runtimeGrantTestClock{}
	g, err := AcquireRuntimeGrantV3(t.Context(), func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
		clock.set(2*time.Second, nil)
		return runtimeGrantReply(a, r), nil
	}, clock, p, a, r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	if remaining, err := g.Remaining(); err != nil || remaining != 7*time.Second {
		t.Fatalf("remaining=%v err=%v", remaining, err)
	}
	if err := g.CheckBinding(r.Executor, r.Purpose, r.TransportID); err != nil {
		t.Fatal(err)
	}
	if err := g.CheckBinding(r.Executor, AttemptGrantExecuteV3, r.TransportID); err == nil {
		t.Fatal("wrong purpose accepted")
	}
	if err := g.CheckBinding(r.Executor, r.Purpose, "other"); err == nil {
		t.Fatal("wrong transport accepted")
	}
	if g.Request() != r {
		t.Fatal("request binding changed")
	}
	clock.set(9*time.Second, nil)
	if err := g.Check(); !errors.Is(err, ErrRuntimeGrantExpiredV3) {
		t.Fatalf("suspend expiry: %v", err)
	}
	clock.set(2*time.Second, nil)
	if err := g.Check(); err == nil {
		t.Fatal("expired grant revived")
	}
}

func TestRuntimeGrantRejectsInvalidReplies(t *testing.T) {
	cases := map[string]func(*AttemptGrantV3){
		"namespace":   func(g *AttemptGrantV3) { g.Request.Executor.Epoch++ },
		"purpose":     func(g *AttemptGrantV3) { g.Request.Purpose = AttemptGrantExecuteV3 },
		"transport":   func(g *AttemptGrantV3) { g.Request.TransportID = "other" },
		"node":        func(g *AttemptGrantV3) { g.Request.NodeID++ },
		"owner":       func(g *AttemptGrantV3) { g.Authority.OwnerID = "other" },
		"authority":   func(g *AttemptGrantV3) { g.Authority.Epoch++ },
		"revoked":     func(g *AttemptGrantV3) { g.Authority.State = AttemptDrainingV3 },
		"nonpositive": func(g *AttemptGrantV3) { g.NotAfter = g.IssuedAt },
		"over-policy": func(g *AttemptGrantV3) { g.NotAfter = g.NotAfter.Add(time.Second) },
		"lease-bound": func(g *AttemptGrantV3) { g.Authority.LeaseExpiresAt = g.NotAfter.Add(-time.Second) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a, r, p := runtimeGrantFixture()
			_, err := AcquireRuntimeGrantV3(t.Context(), func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
				g := runtimeGrantReply(a, r)
				mutate(&g)
				return g, nil
			}, &runtimeGrantTestClock{}, p, a, r)
			if err == nil {
				t.Fatal("invalid grant accepted")
			}
		})
	}
	for _, elapsed := range []time.Duration{9 * time.Second, 10 * time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			a, r, p := runtimeGrantFixture()
			clock := &runtimeGrantTestClock{}
			_, err := AcquireRuntimeGrantV3(t.Context(), func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
				clock.set(elapsed, nil)
				return runtimeGrantReply(a, r), nil
			}, clock, p, a, r)
			if !errors.Is(err, ErrRuntimeGrantExpiredV3) {
				t.Fatalf("late reply: %v", err)
			}
		})
	}
}

func TestRuntimeGrantWatchdogDuringBlockedRenewal(t *testing.T) {
	a, r, p := runtimeGrantFixture()
	clock := &runtimeGrantTestClock{}
	entered := make(chan struct{})
	returned := make(chan struct{})
	var calls atomic.Int32
	g, err := AcquireRuntimeGrantV3(t.Context(), func(ctx context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
		if calls.Add(1) > 1 {
			close(entered)
			<-ctx.Done()
			close(returned)
		}
		return runtimeGrantReply(a, r), nil // even a late successful reply cannot revive authority
	}, clock, p, a, r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	clock.set(6*time.Second, nil)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("renewal did not start")
	}
	clock.set(10*time.Second, nil)
	select {
	case <-g.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("blocked renewal prevented expiry")
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("renewal context not canceled")
	}
	if g.Check() == nil {
		t.Fatal("late renewal revived controller")
	}
	if calls.Load() != 2 {
		t.Fatalf("renewals not serialized: calls=%d", calls.Load())
	}
}

func TestRuntimeGrantRenewalFailureAndClockFailure(t *testing.T) {
	for _, kind := range []string{"source", "clock", "backwards", "disconnect"} {
		t.Run(kind, func(t *testing.T) {
			a, r, p := runtimeGrantFixture()
			clock := &runtimeGrantTestClock{now: time.Second}
			var calls atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			g, err := AcquireRuntimeGrantV3(ctx, func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
				if calls.Add(1) > 1 {
					return AttemptGrantV3{}, errors.New("source disconnected")
				}
				return runtimeGrantReply(a, r), nil
			}, clock, p, a, r)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(g.Close)
			switch kind {
			case "source":
				clock.set(7*time.Second, nil)
			case "clock":
				clock.set(2*time.Second, errors.New("clock failed"))
			case "backwards":
				clock.set(0, nil)
			case "disconnect":
				cancel()
			}
			select {
			case <-g.Context().Done():
			case <-time.After(time.Second):
				t.Fatal("failure did not cancel grant")
			}
			if g.Check() == nil {
				t.Fatal("failed controller remained usable")
			}
		})
	}
}

func TestRuntimeGrantSuccessfulRenewal(t *testing.T) {
	a, r, p := runtimeGrantFixture()
	clock := &runtimeGrantTestClock{}
	g, err := AcquireRuntimeGrantV3(t.Context(), func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
		return runtimeGrantReply(a, r), nil
	}, clock, p, a, r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	clock.set(5*time.Second, nil)
	if err := g.acquire(true); err != nil {
		t.Fatal(err)
	}
	if remaining, err := g.Remaining(); err != nil || remaining != 9*time.Second {
		t.Fatalf("renewed budget=%v err=%v", remaining, err)
	}
	g.Close()
	if err := g.acquire(true); err == nil {
		t.Fatal("closed grant renewed")
	}
}

func TestRuntimeGrantSystemClock(t *testing.T) {
	clock, err := NewRuntimeGrantClockV3()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		if err == nil {
			t.Fatal("unsupported platform supplied a clock")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	first, err := clock.Now()
	if err != nil {
		t.Fatal(err)
	}
	second, err := clock.Now()
	if err != nil || second < first {
		t.Fatalf("system elapsed clock: %v %v %v", first, second, err)
	}
}

func TestRuntimeGrantPreparingExecuteAndInitialTimeout(t *testing.T) {
	a, r, p := runtimeGrantFixture()
	a.State = AttemptPreparingV3
	r.Purpose = AttemptGrantExecuteV3
	g, err := AcquireRuntimeGrantV3(t.Context(), func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
		return runtimeGrantReply(a, r), nil
	}, &runtimeGrantTestClock{}, p, a, r)
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	r.Purpose = AttemptGrantServeV3
	if _, err := AcquireRuntimeGrantV3(t.Context(), func(_ context.Context, a AttemptAuthorityV3, r AttemptGrantRequestV3) (AttemptGrantV3, error) {
		return runtimeGrantReply(a, r), nil
	}, &runtimeGrantTestClock{}, p, a, r); err == nil {
		t.Fatal("preparing serve accepted")
	}
	a.State = AttemptActiveV3
	p = RuntimeGrantPolicyV3{MaxDuration: 20 * time.Millisecond, SafetyMargin: 2 * time.Millisecond, RenewBefore: 5 * time.Millisecond, PollInterval: time.Millisecond}
	r.Duration = p.MaxDuration
	_, err = AcquireRuntimeGrantV3(t.Context(), func(ctx context.Context, _ AttemptAuthorityV3, _ AttemptGrantRequestV3) (AttemptGrantV3, error) {
		<-ctx.Done()
		return AttemptGrantV3{}, ctx.Err()
	}, &runtimeGrantTestClock{}, p, a, r)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("initial source timeout: %v", err)
	}
}

func TestRuntimeGrantPolicyReservesWatchdogMargin(t *testing.T) {
	_, _, policy := runtimeGrantFixture()
	policy.PollInterval = policy.SafetyMargin
	if policy.Validate() == nil {
		t.Fatal("watchdog interval consumed the entire safety margin")
	}
}
