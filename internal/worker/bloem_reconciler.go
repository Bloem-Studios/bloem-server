package worker

import (
	"context"
	"time"
)

func (r *Reconciler) run() {
	defer close(r.loopDone)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.lifecycleCtx.Done():
			return
		case <-ticker.C:
			if r.lifecycleCtx.Err() != nil {
				return
			}
			r.tick()
		}
	}
}

// StopAndWait stops the reconciler and waits for both its ticker loop and the
// reconciliation owner that was already in flight. A wait-context error does
// not consume either completion signal; a later caller can still join.
func (r *Reconciler) StopAndWait(ctx context.Context) error {
	r.Stop()

	r.syncMu.Lock()
	var ownerDone <-chan struct{}
	if r.syncRunning {
		ownerDone = r.syncOwnerDone
	}
	r.syncMu.Unlock()

	if err := waitForReconcilerCompletion(ctx, r.loopDone); err != nil {
		return err
	}
	if ownerDone != nil {
		return waitForReconcilerCompletion(ctx, ownerDone)
	}
	return nil
}

func waitForReconcilerCompletion(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	default:
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		select {
		case <-done:
			return nil
		default:
			return ctx.Err()
		}
	}
}
