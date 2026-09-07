package playback

import "context"

// PublishBoundSessionReplacement projects an already committed successor. It
// never recreates a missing session or changes source/sink authority. Callers
// must complete durable route replacement before invoking this local CAS.
func (m *SessionManager) PublishBoundSessionReplacement(ctx context.Context, binding InitialActivationBindingV3, previous ExecutorNamespaceV3, previousTransport string, next Session) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nextBinding, ok := next.InitialActivationBinding()
	if binding.Validate() != nil || !ok || nextBinding != binding || next.Executor == nil || next.Executor.Validate() != nil || previous.Validate() != nil || previousTransport == "" || next.TranscodeTransportID == "" || next.TranscodeTransportID == previousTransport || *next.Executor == previous || next.Executor.Incarnation != binding.Fence.Incarnation || next.Executor.Epoch != binding.Fence.Epoch || next.ID != binding.Scope.SessionID || next.UserID != binding.Source.AccountID || next.ProfileID != binding.Scope.ProfileID {
		return nil, ErrInitialActivationConflictV3
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.sessions[binding.Scope.SessionID]
	if current == nil {
		return nil, ErrSessionNotFound
	}
	currentBinding, ok := current.InitialActivationBinding()
	if !ok || currentBinding != binding || current.Executor == nil {
		return nil, ErrInitialActivationConflictV3
	}
	if *current.Executor == *next.Executor && current.TranscodeTransportID == next.TranscodeTransportID {
		return cloneInitialSession(current), nil
	}
	if *current.Executor != previous || current.TranscodeTransportID != previousTransport {
		return nil, ErrInitialActivationConflictV3
	}
	// Apply only route state and the explicit local seek position. Preserve
	// current connection counts, pause state and all immutable session identity.
	applySessionStreamStateLocked(current, snapshotSessionStreamStateLocked(&next))
	current.Executor = new(*next.Executor)
	current.MediaFileID = next.MediaFileID
	current.Position = next.Position
	current.streamRevision++
	m.touchSessionLocked(current)
	return cloneInitialSession(current), nil
}
