package playback

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func ownerLeaseTestReply(a AttemptAuthorityV3, duration time.Duration) AttemptLeaseV3 {
	issued := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	a.LeaseExpiresAt = issued.Add(duration)
	return AttemptLeaseV3{Authority: a, IssuedAt: issued}
}

func TestRuntimeOwnerLeaseRoundTripAndLongPersistedLease(t *testing.T) {
	a, _, p := runtimeGrantFixture()
	clock := &runtimeGrantTestClock{}
	s, err := AcquireRuntimeOwnerLeaseV3(t.Context(), func(_ context.Context, a AttemptAuthorityV3, d time.Duration) (AttemptLeaseV3, error) {
		clock.set(2*time.Second, nil)
		return ownerLeaseTestReply(a, 2*d), nil
	}, clock, p, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if remaining, err := s.Remaining(); err != nil || remaining != 7*time.Second {
		t.Fatalf("capped roundtrip budget=%v err=%v", remaining, err)
	}
	if !s.Authority().LeaseExpiresAt.Equal(ownerLeaseTestReply(a, 20*time.Second).Authority.LeaseExpiresAt) {
		t.Fatal("persisted lease snapshot was shortened")
	}
	clock.set(9*time.Second, nil)
	if err := s.Check(); !errors.Is(err, ErrRuntimeOwnerLeaseExpiredV3) {
		t.Fatalf("suspend expiry=%v", err)
	}
	clock.set(0, nil)
	if s.Check() == nil {
		t.Fatal("expired owner revived")
	}
}

func TestRuntimeOwnerLeasePreparingActiveRegression(t *testing.T) {
	a, _, p := runtimeGrantFixture()
	a.State = AttemptPreparingV3
	clock := &runtimeGrantTestClock{}
	var mu sync.Mutex
	state := AttemptPreparingV3
	s, err := AcquireRuntimeOwnerLeaseV3(t.Context(), func(_ context.Context, captured AttemptAuthorityV3, d time.Duration) (AttemptLeaseV3, error) {
		mu.Lock()
		defer mu.Unlock()
		captured.State = state
		return ownerLeaseTestReply(captured, d), nil
	}, clock, p, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	mu.Lock()
	state = AttemptActiveV3
	mu.Unlock()
	clock.set(5*time.Second, nil)
	if err := s.acquire(true); err != nil {
		t.Fatal(err)
	}
	if s.Authority().State != AttemptActiveV3 {
		t.Fatal("activation not retained")
	}
	mu.Lock()
	state = AttemptPreparingV3
	mu.Unlock()
	if err := s.acquire(true); err == nil {
		t.Fatal("active owner regressed to preparing")
	}
	if s.Check() == nil {
		t.Fatal("state regression did not close supervisor")
	}
}

func TestRuntimeOwnerLeaseRejectsOwnershipTransitions(t *testing.T) {
	changes := map[string]func(*AttemptAuthorityV3){
		"attempt":     func(a *AttemptAuthorityV3) { a.PlaybackAttemptID = "replacement" },
		"incarnation": func(a *AttemptAuthorityV3) { a.Incarnation = "replacement" },
		"boot":        func(a *AttemptAuthorityV3) { a.OwnerID = "replacement" },
		"epoch":       func(a *AttemptAuthorityV3) { a.Epoch++ },
		"draining":    func(a *AttemptAuthorityV3) { a.State = AttemptDrainingV3 },
		"stopped":     func(a *AttemptAuthorityV3) { a.State = AttemptStoppedV3 },
		"terminal":    func(a *AttemptAuthorityV3) { a.State = AttemptTerminalV3 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			a, _, p := runtimeGrantFixture()
			clock := &runtimeGrantTestClock{}
			var calls atomic.Int32
			s, err := AcquireRuntimeOwnerLeaseV3(t.Context(), func(_ context.Context, captured AttemptAuthorityV3, d time.Duration) (AttemptLeaseV3, error) {
				if calls.Add(1) > 1 {
					change(&captured)
				}
				return ownerLeaseTestReply(captured, d), nil
			}, clock, p, a)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			clock.set(6*time.Second, nil)
			select {
			case <-s.Context().Done():
			case <-time.After(time.Second):
				t.Fatal("transition did not cancel owner")
			}
			if s.Authority().OwnerID != a.OwnerID || s.Authority().Epoch != a.Epoch || s.Authority().Incarnation != a.Incarnation {
				t.Fatal("replacement identity adopted")
			}
			if s.Check() == nil {
				t.Fatal("old owner survived transition")
			}
		})
	}
}

func TestRuntimeOwnerLeaseBlockedRenewalExpiresIndependently(t *testing.T) {
	a, _, p := runtimeGrantFixture()
	clock := &runtimeGrantTestClock{}
	entered := make(chan struct{})
	finished := make(chan struct{})
	var calls atomic.Int32
	s, err := AcquireRuntimeOwnerLeaseV3(t.Context(), func(ctx context.Context, captured AttemptAuthorityV3, d time.Duration) (AttemptLeaseV3, error) {
		if calls.Add(1) > 1 {
			close(entered)
			<-ctx.Done()
			close(finished)
		}
		return ownerLeaseTestReply(captured, d), nil
	}, clock, p, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	clock.set(6*time.Second, nil)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("renewal not entered")
	}
	clock.set(10*time.Second, nil)
	select {
	case <-s.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("watchdog blocked by source")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("renewal not canceled")
	}
	if s.Check() == nil {
		t.Fatal("late success revived owner")
	}
	if calls.Load() != 2 {
		t.Fatalf("renewal calls=%d", calls.Load())
	}
}

func TestRuntimeOwnerLeaseFailureIsTerminal(t *testing.T) {
	for _, kind := range []string{"late", "source", "clock", "backwards", "disconnect", "zero-interval"} {
		t.Run(kind, func(t *testing.T) {
			a, _, p := runtimeGrantFixture()
			clock := &runtimeGrantTestClock{now: time.Second}
			var calls atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s, err := AcquireRuntimeOwnerLeaseV3(ctx, func(_ context.Context, captured AttemptAuthorityV3, d time.Duration) (AttemptLeaseV3, error) {
				if calls.Add(1) > 1 {
					switch kind {
					case "late":
						clock.set(11*time.Second, nil)
					case "source":
						return AttemptLeaseV3{}, errors.New("database unavailable")
					case "zero-interval":
						d = 0
					}
				}
				return ownerLeaseTestReply(captured, d), nil
			}, clock, p, a)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			switch kind {
			case "clock":
				clock.set(2*time.Second, errors.New("clock unavailable"))
			case "backwards":
				clock.set(0, nil)
			case "disconnect":
				cancel()
			default:
				clock.set(7*time.Second, nil)
			}
			select {
			case <-s.Context().Done():
			case <-time.After(time.Second):
				t.Fatal("failure did not cancel owner")
			}
			if s.acquire(true) == nil {
				t.Fatal("failed owner renewed")
			}
		})
	}
}

func TestRuntimeOwnerLeaseInitialTimeoutAndLateReply(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "late"}[late], func(t *testing.T) {
			a, _, _ := runtimeGrantFixture()
			clock := &runtimeGrantTestClock{}
			p := RuntimeGrantPolicyV3{MaxDuration: 20 * time.Millisecond, SafetyMargin: 2 * time.Millisecond, RenewBefore: 5 * time.Millisecond, PollInterval: time.Millisecond}
			_, err := AcquireRuntimeOwnerLeaseV3(t.Context(), func(ctx context.Context, a AttemptAuthorityV3, d time.Duration) (AttemptLeaseV3, error) {
				if late {
					clock.set(18*time.Millisecond, nil)
					return ownerLeaseTestReply(a, d), nil
				}
				<-ctx.Done()
				return AttemptLeaseV3{}, ctx.Err()
			}, clock, p, a)
			if err == nil {
				t.Fatal("unusable initial owner lease accepted")
			}
		})
	}
}
