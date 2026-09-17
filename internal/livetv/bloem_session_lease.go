package livetv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// SessionLeaseInterval is how often a long-lived stream renews or re-checks its
// session. It stays well inside StaleSessionTTL so a stream that is still
// flowing is never reclaimed as abandoned.
const SessionLeaseInterval = StaleSessionTTL / 3

// sessionLeaseCallTimeout bounds each store call a lease makes, so a stalled
// database cannot pin a lease goroutine or delay a stream start indefinitely.
const sessionLeaseCallTimeout = 5 * time.Second

// HoldSessionLease claims a live session for the length of one long-lived
// request (an MPEG-TS proxy), which makes a single fetch and so would otherwise
// never refresh last_seen_at.
//
// The first renewal is synchronous: a session that is missing, released, or
// cannot be read returns an error and a canceled context, so the caller never
// starts copying from a tuner it does not hold. After that the lease renews
// every interval. The returned context is canceled when the session stops being
// active -- released by its owner, an admin, or the stale reclaim on any
// replica -- or when the store cannot confirm it for StaleSessionTTL. Call the
// cancel func when the stream ends.
func (s *Service) HoldSessionLease(ctx context.Context, sessionID string) (context.Context, context.CancelFunc, error) {
	return s.holdSessionLease(ctx, sessionID, SessionLeaseInterval)
}

func (s *Service) holdSessionLease(ctx context.Context, sessionID string, interval time.Duration) (context.Context, context.CancelFunc, error) {
	leaseCtx, cancel := context.WithCancel(ctx)
	if err := s.requireStore(); err != nil {
		cancel()
		return leaseCtx, cancel, err
	}
	if sessionID == "" || interval <= 0 {
		cancel()
		return leaseCtx, cancel, ErrNotFound
	}
	if err := s.renewSessionLease(leaseCtx, sessionID); err != nil {
		cancel()
		return leaseCtx, cancel, err
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastConfirmed := s.now()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
			}
			err := s.renewSessionLease(leaseCtx, sessionID)
			switch {
			case err == nil:
				lastConfirmed = s.now()
				continue
			case leaseCtx.Err() != nil:
				return
			case errors.Is(err, ErrNotFound):
				slog.InfoContext(leaseCtx, "livetv session lease lost; ending stream",
					"session_id", sessionID)
			case s.now().Sub(lastConfirmed) < StaleSessionTTL:
				// A transient store error must not cut a healthy stream.
				slog.WarnContext(leaseCtx, "livetv session lease renew failed",
					"session_id", sessionID, "error", err)
				continue
			default:
				slog.WarnContext(leaseCtx, "livetv session lease unconfirmable; ending stream",
					"session_id", sessionID, "error", err)
			}
			cancel()
			return
		}
	}()
	return leaseCtx, cancel, nil
}

// renewSessionLease confirms sessionID is active and refreshes last_seen_at.
// A missing or inactive session is ErrNotFound.
func (s *Service) renewSessionLease(ctx context.Context, sessionID string) error {
	callCtx, cancel := context.WithTimeout(ctx, sessionLeaseCallTimeout)
	defer cancel()
	session, err := s.store.GetSession(callCtx, sessionID)
	if err != nil {
		return fmt.Errorf("livetv session lease lookup: %w", err)
	}
	if session == nil || session.Status != "active" {
		return ErrNotFound
	}
	return s.TouchSession(callCtx, sessionID)
}
