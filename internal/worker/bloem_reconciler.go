package worker

import (
	"context"
	"sync"
	"time"
)

// bloemRecon is the Bloem reconciler lifecycle embedded in Reconciler. It
// replaces the upstream stop channel with a cancellable lifecycle so Stop
// cancels an in-flight tick, Start/Stop are idempotent, and StopAndWait can
// join both the ticker loop and the in-flight sync owner.
type bloemRecon struct {
	lifecycleCtx context.Context
	cancel       context.CancelFunc
	startOnce    sync.Once
	stopOnce     sync.Once
	loopDone     chan struct{}

	// syncStopped and syncOwnerDone are guarded by Reconciler.syncMu. They
	// fence new ownership during shutdown and expose completion for the
	// owner that was already in flight when shutdown began.
	syncStopped   bool
	syncOwnerDone chan struct{}
}

func newBloemRecon() bloemRecon {
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	return bloemRecon{
		lifecycleCtx: lifecycleCtx,
		cancel:       cancel,
		loopDone:     make(chan struct{}),
	}
}

// bloemStart begins the background reconciliation loop exactly once. It
// reports false only for a reconciler built without the Bloem lifecycle, in
// which case Start falls back to the upstream loop.
func (r *Reconciler) bloemStart() bool {
	if r.loopDone == nil {
		return false
	}
	r.startOnce.Do(func() { go r.run() })
	return true
}

// bloemStop fences new reconciliation ownership, suppresses queued follow-up
// passes, and signals the background loop to stop. It is safe to call
// repeatedly. Use StopAndWait before deleting rows the reconciler can write.
func (r *Reconciler) bloemStop() bool {
	if r.loopDone == nil {
		return false
	}
	r.stopOnce.Do(func() {
		r.syncMu.Lock()
		r.syncStopped = true
		r.syncPending = false
		r.syncMu.Unlock()

		r.cancel()
		// StopAndWait also works before Start and future Start calls are harmless.
		r.startOnce.Do(func() { close(r.loopDone) })
	})
	return true
}

// claimBloemSyncOwner is called by SyncNow, with syncMu held, when it takes
// sync ownership. It publishes an owner-completion channel for StopAndWait
// and returns the release SyncNow defers: ownership (syncRunning) is given up
// only once the owner has fully finished, and completion is signalled then.
func (r *Reconciler) claimBloemSyncOwner() func() {
	ownerDone := make(chan struct{})
	r.syncOwnerDone = ownerDone
	return func() {
		r.syncMu.Lock()
		r.syncRunning = false
		if r.syncStopped {
			r.syncPending = false
		}
		close(ownerDone)
		r.syncMu.Unlock()
	}
}

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
