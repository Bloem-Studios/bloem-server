package playback

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

var ErrRuntimeGrantExpiredV3 = errors.New("runtime grant expired")
var ErrRuntimeGrantClosedV3 = errors.New("runtime grant closed")

// RuntimeGrantClockV3 measures elapsed time including machine suspend. Clock
// errors and backwards readings are fatal; wall time is never substituted.
type RuntimeGrantClockV3 interface{ Now() (time.Duration, error) }

// RuntimeGrantSourceV3 must honor context cancellation. A separate watchdog
// cancels authority even while a source request is blocked.
type RuntimeGrantSourceV3 func(context.Context, AttemptAuthorityV3, AttemptGrantRequestV3) (AttemptGrantV3, error)

type ExecutorGrantProviderV3 func(context.Context, string, ExecutorNamespaceV3, AttemptGrantPurposeV3) (*RuntimeGrantV3, error)

// RuntimeGrantPolicyV3 has no production defaults. The safety margin covers
// bounded clock-rate error and deadline conversion/dispatch uncertainty.
type RuntimeGrantPolicyV3 struct {
	MaxDuration  time.Duration
	SafetyMargin time.Duration
	RenewBefore  time.Duration
	PollInterval time.Duration
}

func (p RuntimeGrantPolicyV3) Validate() error {
	if p.MaxDuration <= 0 || p.SafetyMargin <= 0 || p.RenewBefore <= 0 || p.PollInterval <= 0 ||
		p.SafetyMargin >= p.MaxDuration || p.RenewBefore >= p.MaxDuration-p.SafetyMargin || p.PollInterval >= p.RenewBefore || p.PollInterval >= p.SafetyMargin {
		return errors.New("invalid runtime grant timing policy")
	}
	return nil
}

// RuntimeGrantV3 is a locally enforced lease. Possessing a namespace alone
// does not create one. Expiry, Close and renewal failure are irreversible.
type RuntimeGrantV3 struct {
	ctx       context.Context
	cancel    context.CancelCauseFunc
	source    RuntimeGrantSourceV3
	clock     RuntimeGrantClockV3
	policy    RuntimeGrantPolicyV3
	authority AttemptAuthorityV3
	request   AttemptGrantRequestV3
	mu        sync.Mutex
	last      time.Duration
	deadline  time.Duration
}

func AcquireRuntimeGrantV3(ctx context.Context, source RuntimeGrantSourceV3, clock RuntimeGrantClockV3, policy RuntimeGrantPolicyV3, authority AttemptAuthorityV3, request AttemptGrantRequestV3) (*RuntimeGrantV3, error) {
	if ctx == nil || source == nil || clock == nil {
		return nil, errors.New("runtime grant dependencies required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if err := request.Executor.Validate(); err != nil {
		return nil, err
	}
	if request.Duration <= policy.SafetyMargin+policy.RenewBefore || request.Duration > policy.MaxDuration ||
		request.SessionID == "" || request.PlanID == "" || request.TransportID == "" || request.NodeID < 0 ||
		(request.Purpose != AttemptGrantExecuteV3 && request.Purpose != AttemptGrantServeV3) ||
		authority.PlaybackAttemptID == "" || authority.OwnerID == "" || !runtimeGrantStateAllowed(authority.State, request.Purpose) ||
		authority.Incarnation != request.Executor.Incarnation || authority.Epoch != request.Executor.Epoch {
		return nil, errors.New("invalid runtime grant request or authority")
	}
	lifetime, cancel := context.WithCancelCause(ctx)
	g := &RuntimeGrantV3{ctx: lifetime, cancel: cancel, source: source, clock: clock, policy: policy, authority: authority, request: request}
	if err := g.acquire(false); err != nil {
		cancel(err)
		return nil, err
	}
	go g.watch()
	go g.renew()
	return g, nil
}

func (g *RuntimeGrantV3) Context() context.Context       { return g.ctx }
func (g *RuntimeGrantV3) Request() AttemptGrantRequestV3 { return g.request }
func (g *RuntimeGrantV3) Close()                         { g.cancel(ErrRuntimeGrantClosedV3) }

func (g *RuntimeGrantV3) CheckBinding(executor ExecutorNamespaceV3, purpose AttemptGrantPurposeV3, transportID string) error {
	if g == nil {
		return ErrRuntimeGrantClosedV3
	}
	if g.request.Executor != executor || g.request.Purpose != purpose || g.request.TransportID != transportID {
		return ErrExecutorNamespaceMismatch
	}
	return g.Check()
}

func (g *RuntimeGrantV3) Check() error { _, err := g.Remaining(); return err }

// Remaining rechecks the suspend-inclusive clock before a guarded effect.
// Ordinary Go timers only schedule checks; they never prove lease validity.
func (g *RuntimeGrantV3) Remaining() (time.Duration, error) {
	if g == nil {
		return 0, ErrRuntimeGrantClosedV3
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now, err := g.nowLocked()
	if err != nil {
		return 0, err
	}
	if now >= g.deadline {
		g.cancel(ErrRuntimeGrantExpiredV3)
		return 0, ErrRuntimeGrantExpiredV3
	}
	return g.deadline - now, nil
}

func (g *RuntimeGrantV3) nowLocked() (time.Duration, error) {
	if err := context.Cause(g.ctx); err != nil {
		return 0, err
	}
	now, err := g.clock.Now()
	if err == nil && (now < 0 || now < g.last) {
		err = errors.New("runtime grant elapsed clock moved backwards")
	}
	if err != nil {
		g.cancel(err)
		return 0, err
	}
	g.last = now
	return now, nil
}

func (g *RuntimeGrantV3) acquire(renewal bool) error {
	g.mu.Lock()
	start, err := g.nowLocked()
	if err == nil && renewal && start >= g.deadline {
		err = ErrRuntimeGrantExpiredV3
		g.cancel(err)
	}
	g.mu.Unlock()
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(g.ctx, g.policy.MaxDuration)
	grant, err := g.source(requestCtx, g.authority, g.request)
	if err == nil {
		err = requestCtx.Err()
	}
	cancel()
	if err != nil {
		g.cancel(err)
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now, err := g.nowLocked()
	if err != nil {
		return err
	}
	if renewal && now >= g.deadline {
		g.cancel(ErrRuntimeGrantExpiredV3)
		return ErrRuntimeGrantExpiredV3
	}
	interval := grant.NotAfter.Sub(grant.IssuedAt)
	if grant.Request != g.request || grant.Authority.PlaybackAttemptID != g.authority.PlaybackAttemptID ||
		grant.Authority.OwnerID != g.authority.OwnerID || grant.Authority.Incarnation != g.authority.Incarnation ||
		grant.Authority.Epoch != g.authority.Epoch || !runtimeGrantStateAllowed(grant.Authority.State, g.request.Purpose) ||
		grant.Authority.LeaseExpiresAt.IsZero() || grant.NotAfter.After(grant.Authority.LeaseExpiresAt) ||
		grant.IssuedAt.IsZero() || interval <= g.policy.SafetyMargin || interval > g.request.Duration || interval > g.policy.MaxDuration {
		err = errors.New("invalid runtime grant response binding or interval")
		g.cancel(err)
		return err
	}
	budget := interval - g.policy.SafetyMargin
	if start > time.Duration(math.MaxInt64)-budget || now-start >= budget {
		g.cancel(ErrRuntimeGrantExpiredV3)
		return ErrRuntimeGrantExpiredV3
	}
	g.deadline = start + budget
	return nil
}

func runtimeGrantStateAllowed(state AttemptAuthorityStateV3, purpose AttemptGrantPurposeV3) bool {
	return state == AttemptActiveV3 || (purpose == AttemptGrantExecuteV3 && state == AttemptPreparingV3)
}

func (g *RuntimeGrantV3) watch() {
	ticker := time.NewTicker(g.policy.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			if g.Check() != nil {
				return
			}
		}
	}
}

func (g *RuntimeGrantV3) renew() {
	ticker := time.NewTicker(g.policy.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			remaining, err := g.Remaining()
			if err != nil {
				return
			}
			if remaining <= g.policy.RenewBefore {
				if err := g.acquire(true); err != nil {
					return
				}
			}
		}
	}
}

// NewRuntimeGrantClockV3 fails closed if the platform cannot provide a
// suspend-inclusive elapsed clock. No time.Now monotonic fallback is allowed.
func NewRuntimeGrantClockV3() (RuntimeGrantClockV3, error) {
	clock := runtimeGrantSystemClockV3{}
	if _, err := clock.Now(); err != nil {
		return nil, fmt.Errorf("runtime grant clock unavailable: %w", err)
	}
	return clock, nil
}
