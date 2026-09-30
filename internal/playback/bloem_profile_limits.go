package playback

import "context"

// ProfileSessionLimitProvider receives the validated active playback profile.
type ProfileSessionLimitProvider func(context.Context, int, string) (SessionLimits, error)

func (m *SessionManager) SetProfileLimitProvider(provider ProfileSessionLimitProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limitProvider = provider
}
