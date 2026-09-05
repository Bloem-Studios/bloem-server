package historyimport

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

func (s *Service) wakeAdminQueue() {
	select {
	case s.queueWake <- struct{}{}:
	default:
	}
}

func (s *Service) startAdminQueue() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if err := s.repo.reconcileUndispatchedRuns(s.bgContext); err != nil {
				slog.WarnContext(s.bgContext, "history import: queued reconciliation failed", "error", err)
			}
			s.dispatchAdminRuns()
			select {
			case <-s.bgContext.Done():
				return
			case <-s.queueWake:
			case <-ticker.C:
			}
		}
	}()
}

// Capacity is held before touching a queued row, so another node can claim it
// while this node is busy. Queued work has no process-local goroutine/provider.
func (s *Service) dispatchAdminRuns() {
	for {
		if s.bgContext.Err() != nil {
			return
		}
		select {
		case s.runSemaphore <- struct{}{}:
		default:
			return
		}
		run, claim, err := s.repo.claimAdminRun(s.bgContext)
		if err != nil || run == nil {
			<-s.runSemaphore
			if err != nil {
				slog.WarnContext(s.bgContext, "history import: queue claim failed", "error", err)
			}
			return
		}
		go func() {
			defer func() { <-s.runSemaphore; s.wakeAdminQueue() }()
			// Fetch the secret only after a durable claim. The provider keeps it solely
			// in memory and is discarded on exit; restart reconstructs from the source.
			err := s.repo.validateRunClaim(s.bgContext, claim)
			var provider Provider
			if err == nil {
				source, token, loadErr := s.repo.GetSourceWithAdminToken(s.bgContext, claim.SourceID)
				err = loadErr
				if err == nil && source.Revision != claim.SourceRevision {
					err = ErrRunConfigurationChanged
				}
				if err == nil {
					provider, err = s.buildAdminProvider(source, token, claim.ExternalUserID)
				}
			}
			if err != nil {
				s.failClaim(s.bgContext, claim, ExecutionSummary{}, err)
				return
			}
			s.executeRunWithClaim(run, provider, claim, true)
		}()
	}
}

func (s *Service) startClaimHeartbeat(ctx context.Context, claim RunClaim, cancel context.CancelCauseFunc) context.CancelFunc {
	hbCtx, stop := context.WithCancel(ctx)
	go heartbeatLoop(hbCtx, historyImportHeartbeatInterval, func(ctx context.Context) error {
		err := s.repo.touchClaimHeartbeat(ctx, claim)
		if err != nil {
			cancel(err)
		}
		return err
	})
	return stop
}

func (s *Service) failClaim(ctx context.Context, claim RunClaim, summary ExecutionSummary, cause error) {
	if cancelledCause := context.Cause(ctx); cancelledCause != nil {
		cause = cancelledCause
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// Cancellation is durable: worker acknowledgement wins over a simultaneous
	// provider error, and no summary/terminal write may erase the request.
	if err := s.repo.acknowledgeRunCancellation(finishCtx, claim); err == nil {
		s.notifyRunByID(finishCtx, claim.RunID)
		return
	}
	if errors.Is(cause, ErrRunClaimLost) {
		return
	}
	message := userFacingRunError(summary, cause)
	if errors.Is(cause, ErrRunConfigurationChanged) {
		message = ErrRunConfigurationChanged.Error()
	}
	if err := s.repo.failRun(finishCtx, claim, summary, message); err != nil {
		if !errors.Is(err, ErrRunNotFound) {
			slog.WarnContext(finishCtx, "history import: terminal write failed", "run_id", claim.RunID, "error", err)
		}
		return
	}
	s.notifyRunByID(finishCtx, claim.RunID)
}

func (s *Service) ListAdminRunsPage(ctx context.Context, sourceID *int, after *RunKey, limit int) ([]Run, bool, error) {
	return s.repo.ListAdminRunsPage(ctx, sourceID, after, limit)
}
