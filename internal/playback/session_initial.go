package playback

import (
	"context"
	"time"
)

// InitialActivationBinding returns immutable captured authority without exposing
// the manager's binding storage to callers holding detached session snapshots.
func (s *Session) InitialActivationBinding() (InitialActivationBindingV3, bool) {
	if s == nil || s.initialActivation == nil {
		return InitialActivationBindingV3{}, false
	}
	return *s.initialActivation, true
}

func (m *SessionManager) StageInitialSession(ctx context.Context, binding InitialActivationBindingV3, effectiveFileID, requestedFileID int, method PlayMethod, transcodeAudio bool) (*Session, error) {
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	if effectiveFileID <= 0 || requestedFileID <= 0 || (method != PlayDirect && method != PlayRemux && method != PlayTranscode) {
		return nil, ErrInitialActivationInvalidV3
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	existing, err := m.findInitialSessionLocked(binding, effectiveFileID, requestedFileID, method, transcodeAudio)
	m.mu.Unlock()
	if existing != nil || err != nil {
		return existing, err
	}
	return m.startSessionWithInitialBinding(ctx, binding.Source.AccountID, binding.Scope.ProfileID, effectiveFileID, requestedFileID, method, transcodeAudio, &binding)
}

func cloneInitialSession(s *Session) *Session {
	cp := *s
	if s.Executor != nil {
		cp.Executor = new(*s.Executor)
	}
	if s.initialActivation != nil {
		cp.initialActivation = new(*s.initialActivation)
	}
	return &cp
}

func initialSessionMatches(s *Session, binding InitialActivationBindingV3, effectiveFileID, requestedFileID int, method PlayMethod, transcodeAudio bool) bool {
	actual, ok := s.InitialActivationBinding()
	return ok && actual == binding && s.ID == binding.Scope.SessionID && s.UserID == binding.Source.AccountID && s.ProfileID == binding.Scope.ProfileID && s.MediaFileID == effectiveFileID && s.RequestedMediaFileID == requestedFileID && s.PlayMethod == method && s.BasePlayMethod == method && s.TranscodeAudio == transcodeAudio
}

func (m *SessionManager) findInitialSessionLocked(binding InitialActivationBindingV3, effectiveFileID, requestedFileID int, method PlayMethod, transcodeAudio bool) (*Session, error) {
	s := m.stagedInitial[binding.Scope.SessionID]
	if s == nil {
		s = m.sessions[binding.Scope.SessionID]
	}
	if s == nil {
		return nil, nil
	}
	if !initialSessionMatches(s, binding, effectiveFileID, requestedFileID, method, transcodeAudio) {
		return nil, ErrInitialActivationConflictV3
	}
	return cloneInitialSession(s), nil
}

func (m *SessionManager) admitInitialSessionLocked(s *Session, binding *InitialActivationBindingV3) *Session {
	if binding == nil {
		m.sessions[s.ID] = s
		return s
	}
	s.ID = binding.Scope.SessionID
	s.initialActivation = new(*binding)
	if m.stagedInitial == nil {
		m.stagedInitial = make(map[string]*Session)
	}
	m.stagedInitial[s.ID] = s
	return cloneInitialSession(s)
}

// PublishInitialSession installs a configured detached snapshot atomically. It
// performs no durable publication; the caller must complete control publication
// first and reconcile an uncertain control result before making this call.
func (m *SessionManager) PublishInitialSession(ctx context.Context, binding InitialActivationBindingV3, snapshot Session) (*Session, error) {
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !initialSessionMatches(&snapshot, binding, snapshot.MediaFileID, snapshot.RequestedMediaFileID, snapshot.PlayMethod, snapshot.TranscodeAudio) {
		return nil, ErrInitialActivationConflictV3
	}
	if snapshot.Executor != nil {
		if err := snapshot.Executor.Validate(); err != nil {
			return nil, err
		}
		if snapshot.Executor.Incarnation != binding.Fence.Incarnation || snapshot.Executor.Epoch != binding.Fence.Epoch {
			return nil, ErrInitialActivationConflictV3
		}
	}
	s := m.stagedInitial[binding.Scope.SessionID]
	if s == nil {
		s = m.sessions[binding.Scope.SessionID]
		if s == nil || !initialSessionMatches(s, binding, snapshot.MediaFileID, snapshot.RequestedMediaFileID, snapshot.PlayMethod, snapshot.TranscodeAudio) {
			return nil, ErrInitialActivationConflictV3
		}
		return cloneInitialSession(s), nil
	}
	if m.sessions[binding.Scope.SessionID] != nil {
		return nil, ErrInitialActivationConflictV3
	}
	if !initialSessionMatches(s, binding, snapshot.MediaFileID, snapshot.RequestedMediaFileID, snapshot.PlayMethod, snapshot.TranscodeAudio) {
		return nil, ErrInitialActivationConflictV3
	}
	// Liveness and transport accounting belong to the manager, not configuration.
	snapshot.StartedAt = s.StartedAt
	snapshot.UpdatedAt = time.Now()
	snapshot.LastActivityAt = snapshot.UpdatedAt
	snapshot.activeTransportCount = 0
	snapshot.replacementPlayMethod = ""
	snapshot.streamRevision = 0
	snapshot.HasWebSocket = false
	snapshot.HasRealtimeConnection = false
	committed := cloneInitialSession(&snapshot)
	m.sessions[committed.ID] = committed
	delete(m.stagedInitial, committed.ID)
	return cloneInitialSession(committed), nil
}

// DiscardInitialSession releases only an unpublished exact reservation. A
// publication that won the race is retained and reported as a conflict.
func (m *SessionManager) DiscardInitialSession(ctx context.Context, binding InitialActivationBindingV3) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[binding.Scope.SessionID] != nil {
		return ErrInitialActivationConflictV3
	}
	s := m.stagedInitial[binding.Scope.SessionID]
	if s == nil {
		return nil
	}
	actual, ok := s.InitialActivationBinding()
	if !ok || actual != binding {
		return ErrInitialActivationConflictV3
	}
	delete(m.stagedInitial, binding.Scope.SessionID)
	return nil
}
