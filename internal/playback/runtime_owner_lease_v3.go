package playback

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"
)

// AttemptLeaseV3 carries the database issuance time alongside the renewed owner
// lease. It is not an execute or serve grant.
type AttemptLeaseV3 struct {
	Authority AttemptAuthorityV3
	IssuedAt  time.Time
}

// RuntimeOwnerLeaseSourceV3 must honor cancellation and atomically renew only
// the captured attempt, incarnation, owner boot and epoch.
type RuntimeOwnerLeaseSourceV3 func(context.Context, AttemptAuthorityV3, time.Duration) (AttemptLeaseV3, error)

var ErrRuntimeOwnerLeaseExpiredV3 = errors.New("runtime owner lease expired")
var ErrRuntimeOwnerLeaseClosedV3 = errors.New("runtime owner lease closed")

// RuntimeOwnerLeaseV3 supervises one captured ownership generation. Cancellation
// is terminal. This isolated helper performs no takeover or lifecycle effects.
type RuntimeOwnerLeaseV3 struct {
	ctx       context.Context
	cancel    context.CancelCauseFunc
	source    RuntimeOwnerLeaseSourceV3
	clock     RuntimeGrantClockV3
	policy    RuntimeGrantPolicyV3
	mu        sync.Mutex
	authority AttemptAuthorityV3
	last      time.Duration
	deadline  time.Duration
}

func AcquireRuntimeOwnerLeaseV3(ctx context.Context, source RuntimeOwnerLeaseSourceV3, clock RuntimeGrantClockV3, policy RuntimeGrantPolicyV3, authority AttemptAuthorityV3) (*RuntimeOwnerLeaseV3, error) {
	if ctx == nil || source == nil || clock == nil {
		return nil, errors.New("owner lease dependencies required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if authority.PlaybackAttemptID == "" || authority.Incarnation == "" || authority.OwnerID == "" || authority.Epoch <= 0 || !ownerLeaseStateV3(authority.State) {
		return nil, ErrStaleAttemptAuthorityV3
	}
	lifetime, cancel := context.WithCancelCause(ctx)
	s := &RuntimeOwnerLeaseV3{ctx: lifetime, cancel: cancel, source: source, clock: clock, policy: policy, authority: authority}
	if err := s.acquire(false); err != nil {
		cancel(err)
		return nil, err
	}
	go s.watch()
	go s.renew()
	return s, nil
}

func ownerLeaseStateV3(state AttemptAuthorityStateV3) bool {
	return state == AttemptPreparingV3 || state == AttemptActiveV3
}

func (s *RuntimeOwnerLeaseV3) Context() context.Context { return s.ctx }
func (s *RuntimeOwnerLeaseV3) Close()                   { s.cancel(ErrRuntimeOwnerLeaseClosedV3) }

// Authority returns the last accepted database lease, retaining the captured
// identity. Call Check before an effect; this snapshot alone is not permission.
func (s *RuntimeOwnerLeaseV3) Authority() AttemptAuthorityV3 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authority
}
func (s *RuntimeOwnerLeaseV3) Check() error { _, err := s.Remaining(); return err }
func (s *RuntimeOwnerLeaseV3) Remaining() (time.Duration, error) {
	if s == nil {
		return 0, ErrRuntimeOwnerLeaseClosedV3
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now, err := s.nowLocked()
	if err != nil {
		return 0, err
	}
	if now >= s.deadline {
		s.cancel(ErrRuntimeOwnerLeaseExpiredV3)
		return 0, ErrRuntimeOwnerLeaseExpiredV3
	}
	return s.deadline - now, nil
}

func (s *RuntimeOwnerLeaseV3) nowLocked() (time.Duration, error) {
	if err := context.Cause(s.ctx); err != nil {
		return 0, err
	}
	now, err := s.clock.Now()
	if err == nil && (now < 0 || now < s.last) {
		err = errors.New("owner lease elapsed clock moved backwards")
	}
	if err != nil {
		s.cancel(err)
		return 0, err
	}
	s.last = now
	return now, nil
}

func (s *RuntimeOwnerLeaseV3) acquire(renewal bool) error {
	s.mu.Lock()
	start, err := s.nowLocked()
	if err == nil && renewal && start >= s.deadline {
		err = ErrRuntimeOwnerLeaseExpiredV3
		s.cancel(err)
	}
	captured := s.authority
	s.mu.Unlock()
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(s.ctx, s.policy.MaxDuration)
	lease, err := s.source(requestCtx, captured, s.policy.MaxDuration)
	if err == nil {
		err = requestCtx.Err()
	}
	cancel()
	if err != nil {
		s.cancel(err)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now, err := s.nowLocked()
	if err != nil {
		return err
	}
	if renewal && now >= s.deadline {
		s.cancel(ErrRuntimeOwnerLeaseExpiredV3)
		return ErrRuntimeOwnerLeaseExpiredV3
	}
	a := lease.Authority
	interval := a.LeaseExpiresAt.Sub(lease.IssuedAt)
	if a.PlaybackAttemptID != captured.PlaybackAttemptID || a.Incarnation != captured.Incarnation || a.OwnerID != captured.OwnerID || a.Epoch != captured.Epoch ||
		!ownerLeaseStateV3(a.State) || (captured.State == AttemptActiveV3 && a.State == AttemptPreparingV3) ||
		lease.IssuedAt.IsZero() || interval <= s.policy.SafetyMargin {
		err = errors.New("invalid owner lease response identity, state or interval")
		s.cancel(err)
		return err
	}
	// PG may preserve an earlier, longer lease so issued grants are not cut
	// short. Local supervision still never consumes more than this policy cap.
	budget := min(interval, s.policy.MaxDuration) - s.policy.SafetyMargin
	if start > time.Duration(math.MaxInt64)-budget || now-start >= budget {
		s.cancel(ErrRuntimeOwnerLeaseExpiredV3)
		return ErrRuntimeOwnerLeaseExpiredV3
	}
	s.authority = a
	s.deadline = start + budget
	return nil
}

func (s *RuntimeOwnerLeaseV3) watch() {
	ticker := time.NewTicker(s.policy.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if s.Check() != nil {
				return
			}
		}
	}
}

func (s *RuntimeOwnerLeaseV3) renew() {
	ticker := time.NewTicker(s.policy.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			remaining, err := s.Remaining()
			if err != nil {
				return
			}
			if remaining <= s.policy.RenewBefore {
				if err := s.acquire(true); err != nil {
					return
				}
			}
		}
	}
}
