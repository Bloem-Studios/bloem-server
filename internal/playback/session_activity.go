package playback

import (
	"context"
	"log/slog"
	"time"
)

// SessionActivityReader reads persisted activity for the supplied native
// sessions, validating their account/profile ownership. Missing entries carry
// no additional activity. A failed lookup must not authorize session expiry.
type SessionActivityReader func(context.Context, []Session) (map[string]time.Time, error)

// SetDeliveryActivityReader installs the reader for media that another node
// served for a remote-transport session. A remote session otherwise stays alive
// only on its client's progress reports, so a client that pulls media without
// reporting (a Cast receiver whose sender phone went to sleep) would be reaped
// mid-playback.
//
// Unlike compatibility activity, a failed lookup does not protect sessions: the
// sweep falls back to progress reports alone, as it did before this signal
// existed, rather than holding every remote session open during an outage.
func (m *SessionManager) SetDeliveryActivityReader(reader SessionActivityReader) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deliveryActivityReader = reader
}

// refreshDeliveryActivity merges node delivery into every remote-transport
// session, not only the ones about to expire. Their activity then stays as fresh
// as the media they receive, so they keep counting toward stream limits between
// sweeps, a session that goes idle during the lookup was still checked, and the
// paused grace runs from the last delivery however long it is.
func (m *SessionManager) refreshDeliveryActivity(activeIdle, pausedIdle time.Duration) {
	m.mu.RLock()
	reader := m.deliveryActivityReader
	m.mu.RUnlock()
	m.refreshExternalActivity("node_delivery", reader, activeIdle, pausedIdle, false, func(s *Session, _ bool) bool {
		return s.remoteTransport
	})
}

// refreshExternalActivity merges activity recorded outside this replica into
// the sessions that include selects among those with no local transport;
// include is told whether the session is already inactive. When protectOnError
// is set, a failed lookup keeps that batch from expiring in this sweep; the
// returned map lists those sessions.
func (m *SessionManager) refreshExternalActivity(source string, reader SessionActivityReader, activeIdle, pausedIdle time.Duration, protectOnError bool, include func(s *Session, inactive bool) bool) map[*Session]bool {
	if reader == nil {
		return nil
	}
	m.mu.RLock()
	now := time.Now()
	var candidates []Session
	originals := make(map[string]*Session)
	for id, s := range m.sessions {
		active, paused := remoteTransportGrace(s, activeIdle, pausedIdle)
		if s.activeTransportCount == 0 && include(s, m.sessionIsInactiveLocked(s, now, active, paused)) {
			candidates = append(candidates, *s)
			originals[id] = s
		}
	}
	m.mu.RUnlock()
	if len(candidates) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	protected := make(map[*Session]bool)
	for offset := 0; offset < len(candidates); offset += 256 {
		batch := candidates[offset:min(offset+256, len(candidates))]
		activity, err := reader(ctx, batch)
		if err != nil {
			slog.Warn("could not refresh external playback activity", "source", source, "error", err, "sessions", len(batch), "protecting", protectOnError)
		}
		m.mu.Lock()
		for _, candidate := range batch {
			current := m.sessions[candidate.ID]
			if current == nil || current != originals[candidate.ID] {
				continue
			}
			if err != nil {
				if protectOnError {
					protected[current] = true
				}
				continue
			}
			stamp := activity[candidate.ID]
			if stamp.After(current.LastActivityAt) {
				current.LastActivityAt = stamp
				if stamp.After(current.UpdatedAt) {
					current.UpdatedAt = stamp
				}
			}
		}
		m.mu.Unlock()
	}
	return protected
}
