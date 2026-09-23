package jellycompat

import (
	"context"
	"errors"
)

type sessionUserDeletePersistence interface {
	DeleteByUserID(context.Context, int) (int, error)
}

type sessionProfileDeletePersistence interface {
	DeleteByUserAndProfileIDs(context.Context, int, []string) (int, error)
}

// DeleteByUserIDContext removes all compat sessions for a Silo user. Durable
// storage is cleared first so an eviction failure cannot leave a reloadable
// session after its in-memory copy has disappeared.
func (s *SessionStore) DeleteByUserIDContext(ctx context.Context, userID int) error {
	if s.repo != nil {
		repo, ok := s.repo.(sessionUserDeletePersistence)
		if !ok {
			return errors.New("jellycompat session persistence does not support user invalidation")
		}
		if _, err := repo.DeleteByUserID(ctx, userID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	for token, session := range s.sessions {
		if session.StreamAppUserID == userID {
			delete(s.sessions, token)
		}
	}
	s.mu.Unlock()
	return nil
}

// DeleteByUserAndProfileIDs removes only profile-mode sessions attributed to
// the supplied user's profiles. Account-mode and other-profile sessions are
// deliberately retained for multi-organization accounts.
func (s *SessionStore) DeleteByUserAndProfileIDs(ctx context.Context, userID int, profileIDs []string) error {
	profiles := make(map[string]struct{}, len(profileIDs))
	for _, profileID := range profileIDs {
		if profileID != "" {
			profiles[profileID] = struct{}{}
		}
	}
	if len(profiles) == 0 {
		return nil
	}
	canonical := make([]string, 0, len(profiles))
	for profileID := range profiles {
		canonical = append(canonical, profileID)
	}
	if s.repo != nil {
		repo, ok := s.repo.(sessionProfileDeletePersistence)
		if !ok {
			return errors.New("jellycompat session persistence does not support profile invalidation")
		}
		if _, err := repo.DeleteByUserAndProfileIDs(ctx, userID, canonical); err != nil {
			return err
		}
	}
	s.mu.Lock()
	for token, session := range s.sessions {
		if session.StreamAppUserID != userID {
			continue
		}
		if _, ok := profiles[session.ProfileID]; ok {
			delete(s.sessions, token)
		}
	}
	s.mu.Unlock()
	return nil
}
