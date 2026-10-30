package playback

import (
	"context"
	"errors"
)

// StreamLimitObserver hears that a viewer was refused a stream because their
// plan's simultaneous-stream limit was reached. It runs on the refused
// request's path and must return at once.
type StreamLimitObserver interface {
	StreamLimitReached(ctx context.Context, userID int, profileID string)
}

// SetStreamLimitObserver installs the observer; nil removes it.
func (m *SessionManager) SetStreamLimitObserver(observer StreamLimitObserver) {
	m.mu.Lock()
	m.streamLimitObserver = observer
	m.mu.Unlock()
}

// noteStreamLimit tells the observer when a start was refused for the stream
// limit, on either the inline or the policy admission path. It is deferred
// by StartSessionWithFilesContext with that call's error.
func (m *SessionManager) noteStreamLimit(ctx context.Context, userID int, profileID string, errp *error) {
	if errp == nil || !errors.Is(*errp, ErrTooManyStreams) {
		return
	}
	m.mu.RLock()
	observer := m.streamLimitObserver
	m.mu.RUnlock()
	if observer != nil {
		observer.StreamLimitReached(ctx, userID, profileID)
	}
}
