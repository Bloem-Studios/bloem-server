package storageplugin

// Available reports local admission only; no provider is started or contacted.
func (m *Manager) Available() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed
}
