package handlers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// A retained snapshot resolves an uncertain publication on this boot only.
// It cannot reconstruct ownership after a restart or allocate another executor.
type initialPendingPublicationV3 struct {
	mu      sync.Mutex
	binding playback.InitialActivationBindingV3
	session playback.Session
	owner   *playback.RuntimeOwnerLeaseV3
	record  playback.AttemptRecordV3
}

func (h *PlaybackHandler) retainInitialOwnerV3(binding playback.InitialActivationBindingV3, stage *playback.Session, owner *playback.RuntimeOwnerLeaseV3, record playback.AttemptRecordV3) {
	flow := h.initialFlow
	pending := &initialPendingPublicationV3{binding: binding, session: *stage, owner: owner, record: record}
	flow.pending.Store(stage.ID, pending)
	flow.owners.Store(stage.ID, owner)
	runtime := h.tm.GetTranscodeSession(stage.ID)
	// The caller holds a start-work reference until this callback is registered.
	flow.work.Add(1)
	context.AfterFunc(owner.Context(), func() {
		defer flow.work.Done()
		<-owner.Done()
		pending.mu.Lock()
		defer pending.mu.Unlock()
		flow.owners.CompareAndDelete(stage.ID, owner)
		flow.pending.CompareAndDelete(stage.ID, pending)
		// Discard refuses visible sessions. Lease loss does not imply a stop.
		if manager, ok := h.sessionMgr.(initialSessionManagerV3); ok {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = manager.DiscardInitialSession(cleanup, binding)
		}
		if runtime != nil {
			h.tm.CloseTranscodeSessionIf(stage.ID, runtime, "")
		}
	})
}

func (h *PlaybackHandler) recoverInitialPublicationV3(ctx context.Context, record *playback.AttemptRecordV3) (playback.DecisionResponseV3, error) {
	flow := h.initialFlow
	value, ok := flow.pending.Load(record.SessionID)
	if !ok {
		return playback.DecisionResponseV3{}, errors.New("initial playback owner unavailable on this boot")
	}
	pending, ok := value.(*initialPendingPublicationV3)
	if !ok {
		return playback.DecisionResponseV3{}, errors.New("initial playback owner unavailable on this boot")
	}
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.record.PlaybackAttemptID != record.PlaybackAttemptID || pending.record.UserID != record.UserID || pending.record.ProfileID != record.ProfileID || pending.record.RequestDigest != record.RequestDigest {
		return playback.DecisionResponseV3{}, playback.ErrInitialActivationConflictV3
	}
	if err := pending.owner.Check(); err != nil {
		return playback.DecisionResponseV3{}, err
	}
	state, err := flow.Control.ReadInitialActivation(ctx, pending.binding)
	if err != nil {
		return playback.DecisionResponseV3{}, err
	}
	if state.Phase == playback.InitialActivationInstalledV3 {
		if _, err = flow.Control.PublishInitialActivation(ctx, pending.binding, pending.record); err != nil {
			return playback.DecisionResponseV3{}, err
		}
	}
	live, err := flow.Control.GetActivatedPlaybackAuthority(ctx, record.UserID, record.ProfileID, record.SessionID)
	if err != nil {
		return playback.DecisionResponseV3{}, err
	}
	if live.Activation.Phase != playback.InitialActivationActivatedV3 || pending.binding != live.Binding {
		return playback.DecisionResponseV3{}, playback.ErrInitialActivationConflictV3
	}
	manager, ok := h.sessionMgr.(initialSessionManagerV3)
	if !ok {
		return playback.DecisionResponseV3{}, errors.New("staged session manager unavailable")
	}
	_, err = manager.PublishInitialSession(ctx, live.Binding, pending.session)
	if err != nil {
		return playback.DecisionResponseV3{}, err
	}
	return pending.record.StartResponse, nil
}

// startShutdownJoin fences new work before waiting. Work already inside a start
// may register its owner callback while it still holds its own work reference.
func (f *InitialPlaybackFlowV3) startShutdownJoin() {
	f.shutdownDone = make(chan struct{})
	acquire := f.AcquireGrant
	f.AcquireGrant = func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		if !f.beginWork() {
			return nil, errors.New("initial playback is shutting down")
		}
		defer f.work.Done()
		lifetime, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(f.Context, cancel)
		grant, err := acquire(lifetime, transport, executor, purpose)
		if err != nil {
			stop()
			cancel()
			return nil, err
		}
		f.work.Go(func() {
			defer cancel()
			defer stop()
			<-grant.Done()
		})
		return grant, nil
	}
	if open := f.OpenOutputTransfer; open != nil {
		f.OpenOutputTransfer = func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3) (string, func(), error) {
			if !f.beginWork() {
				return "", nil, errors.New("initial playback is shutting down")
			}
			permit, closePermit, err := open(ctx, transport, executor)
			if err != nil || permit == "" || closePermit == nil {
				if closePermit != nil {
					closePermit()
				}
				f.work.Done()
				if err == nil {
					err = errors.New("initial output transfer permit missing")
				}
				return "", nil, err
			}
			return permit, sync.OnceFunc(func() { defer f.work.Done(); closePermit() }), nil
		}
	}

	go func() {
		<-f.Context.Done()
		f.shutdownMu.Lock()
		f.shuttingDown = true
		f.shutdownMu.Unlock()
		f.work.Wait()
		close(f.shutdownDone)
	}()
}

func (f *InitialPlaybackFlowV3) beginWork() bool {
	f.shutdownMu.Lock()
	defer f.shutdownMu.Unlock()
	if f.shuttingDown || f.Context.Err() != nil {
		return false
	}
	f.work.Add(1)
	return true
}

// InitialPlaybackShutdownDone joins starts, owner cleanup and grant supervisors.
// Cancellation never supplies a missing durable stop receipt.
func (h *PlaybackHandler) InitialPlaybackShutdownDone() <-chan struct{} {
	return h.initialFlow.shutdownDone
}
