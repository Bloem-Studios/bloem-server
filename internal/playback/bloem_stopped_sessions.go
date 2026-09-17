package playback

import (
	"context"

	"github.com/google/uuid"
)

// ReapStoppedSessions withdraws local copies of attempts stopped on any API
// replica, including active transports. Dedicated stop hooks tear down the
// producer without treating this as a new expiry or recording history twice.
// Called by the existing reconciliation loop; no per-stream polling goroutine.
func (m *SessionManager) ReapStoppedSessions(ctx context.Context) error {
	m.mu.RLock()
	store, ok := m.reservationStore.(interface {
		StoppedSessions(context.Context, []string) ([]string, error)
	})
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	if !ok || len(ids) == 0 {
		return nil
	}
	ctx, cancel := reservationStoreContext(ctx)
	defer cancel()
	stopped, err := store.StoppedSessions(ctx, ids)
	if err != nil {
		return err
	}
	m.retireStoppedSessions(stopped)
	return nil
}

func (m *SessionManager) retireStoppedSessions(stopped []string) {
	m.mu.Lock()
	var removed []*Session
	for _, id := range stopped {
		if s := m.sessions[id]; s != nil {
			cp := *s
			removed = append(removed, &cp)
			delete(m.sessions, id)
			m.stopTransportsLocked(id)
		}
	}
	hooks := append([]func(*Session){}, m.remoteStopHooks...)
	m.mu.Unlock()
	for _, s := range removed {
		m.releaseFleetReservation(s)
		for _, hook := range hooks {
			hook(s)
		}
	}
}

// AddRemoteStopHook registers local-only teardown after an already-durable stop.
// Unlike an expiry hook, this callback must not record a new history entry.
func (m *SessionManager) AddRemoteStopHook(hook func(*Session)) {
	if hook == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.remoteStopHooks = append(m.remoteStopHooks, hook)
}

func (store *PostgresReservationStore) StoppedSessions(ctx context.Context, ids []string) ([]string, error) {
	// Older compatibility sessions need not have UUID IDs or attempt rows.
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if parsed, err := uuid.Parse(id); err == nil && parsed.String() == id {
			valid = append(valid, id)
		}
	}
	rows, err := store.pool.Query(ctx, `SELECT session_id::text FROM playback_v3_attempts WHERE session_id = ANY($1::uuid[]) AND stopped_at IS NOT NULL`, valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
