package notifications

import (
	"context"
	"sync"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// OpenPlaybackSink forwards the captured reference. It must not call ForUser:
// that would select the current store instead of opening the admitted source.
func (p *interestTrackingProvider) OpenPlaybackSink(ctx context.Context, ref userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
	provider, ok := p.inner.(userstore.PlaybackSourceProvider)
	if !ok {
		return nil, userstore.ErrPlaybackSinkUnsupported
	}
	handle, err := provider.OpenPlaybackSink(ctx, ref)
	if err != nil {
		return nil, err
	}
	if handle == nil {
		return nil, userstore.ErrPlaybackSourceUnavailable
	}
	if handle.Source() != ref {
		_ = handle.Close()
		return nil, userstore.ErrPlaybackSourceMismatch
	}
	return &interestPlaybackSinkHandle{PlaybackSinkHandle: handle, observer: &interestTrackingStore{userID: ref.AccountID, system: p.system, updater: p.system.Interest}}, nil
}

type interestPlaybackSinkHandle struct {
	userstore.PlaybackSinkHandle
	observer *interestTrackingStore
	mu       sync.RWMutex
	closed   bool
	closeErr error
}

func (h *interestPlaybackSinkHandle) ApplyPlaybackProgress(ctx context.Context, request userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return userstore.PlaybackProgressResult{}, userstore.ErrPlaybackSourceClosed
	}
	result, err := h.PlaybackSinkHandle.ApplyPlaybackProgress(ctx, request)
	if err == nil {
		h.observer.queuePlaybackCommit(request.Scope, result)
	}
	return result, err
}
func (h *interestPlaybackSinkHandle) StopPlaybackProgress(ctx context.Context, request userstore.StopPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return userstore.PlaybackProgressResult{}, userstore.ErrPlaybackSourceClosed
	}
	result, err := h.PlaybackSinkHandle.StopPlaybackProgress(ctx, request)
	if err == nil {
		h.observer.queuePlaybackCommit(request.Scope, result)
	}
	return result, err
}

var _ userstore.PlaybackSourceProvider = (*interestTrackingProvider)(nil)

// Close also waits for the after-commit interest hook, not only the inner write.
func (h *interestPlaybackSinkHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		h.closeErr = h.PlaybackSinkHandle.Close()
	}
	return h.closeErr
}
