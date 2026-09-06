package handlers

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type initialOwnerRoundTripClock struct{ elapsed atomic.Int64 }

func (c *initialOwnerRoundTripClock) Now() (time.Duration, error) {
	return time.Duration(c.elapsed.Load()), nil
}

type initialOwnerRoundTripControl struct {
	InitialPlaybackControlV3
	clock    *initialOwnerRoundTripClock
	advanced atomic.Bool
}

func (c *initialOwnerRoundTripControl) RenewAttemptLease(ctx context.Context, authority playback.AttemptAuthorityV3, duration time.Duration) (playback.AttemptLeaseV3, error) {
	lease, err := c.InitialPlaybackControlV3.RenewAttemptLease(ctx, authority, duration)
	if err == nil && c.advanced.CompareAndSwap(false, true) {
		c.clock.elapsed.Add(int64(2 * time.Second))
	}
	return lease, err
}

func TestInitialPlaybackHTTPOwnerRoundTripBudget(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		duration time.Duration
		status   int
	}{
		{"one second expires", time.Second, http.StatusServiceUnavailable},
		{"thirty seconds survives", 30 * time.Second, http.StatusCreated},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newInitialHTTPFixture(t)
			clock := new(initialOwnerRoundTripClock)
			control := &initialOwnerRoundTripControl{InitialPlaybackControlV3: f.flow.Control, clock: clock}
			// Only ownership sees the injected elapsed round trip. ExecutorRuntime keeps
			// its independently constructed real monotonic clock and one-second grants.
			f.flow.Clock = clock
			f.flow.Control = control
			f.flow.Policy.MaxDuration = scenario.duration
			f.flow.Policy.SafetyMargin = 100 * time.Millisecond
			f.flow.Policy.RenewBefore = scenario.duration / 5
			status, data := f.call(t, http.MethodPost, "/start", f.request)
			if !control.advanced.Load() {
				t.Fatal("real database owner renewal was not exercised")
			}
			if status != scenario.status {
				t.Fatalf("owner budget %v: status=%d want=%d body=%s", scenario.duration, status, scenario.status, data)
			}
			if scenario.status == http.StatusCreated && len(f.manager.AllSessions()) != 1 {
				t.Fatal("valid owner failed to expose one runtime")
			}
			if scenario.status == http.StatusServiceUnavailable && len(f.manager.AllSessions()) != 0 {
				t.Fatal("expired owner exposed runtime")
			}
		})
	}
}
