package auth

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

// Bloem-owned. Account refresh re-issues a pair for the session's account. The
// token must name the session's account, and, when it carries one, the
// account's current incarnation: a token minted for an account since replaced
// under the same numeric ID must not refresh as the replacement.
func accountRefreshBindingHolds(claims *Claims, session *models.AuthSession, user *models.User) bool {
	if claims == nil || session == nil || user == nil {
		return false
	}
	if claims.UserID != session.UserID || user.ID != session.UserID {
		return false
	}
	if claims.AccountIncarnationID != "" && claims.AccountIncarnationID != user.AccountIncarnationID.String() {
		return false
	}
	return true
}

// bloemRefreshSession refreshes the session kind Bloem adds, ahead of Silo's
// session checks: a preview guest session never slides. Any other session,
// and one that cannot be read or is no longer live, is left to Refresh,
// which answers for it.
func (s *Service) bloemRefreshSession(ctx context.Context, claims *Claims) (*TokenPair, bool, error) {
	session := s.bloemLiveSession(ctx, claims.SessionID)
	if session == nil {
		return nil, false, nil
	}
	if session.AuthMethod == AuthMethodPreviewGuest {
		pair, err := s.refreshPreviewGuest(ctx, claims)
		return pair, true, err
	}
	return nil, false, nil
}

// bloemLiveSession is the session while it is live, and nil when it cannot be
// read or has ended.
func (s *Service) bloemLiveSession(ctx context.Context, id string) *models.AuthSession {
	session, err := s.bloemSessions.GetByID(ctx, id)
	if err != nil || session.RevokedAt != nil || !session.ExpiresAt.After(time.Now()) {
		return nil
	}
	return session
}
