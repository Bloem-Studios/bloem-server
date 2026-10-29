package auth

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

// Bloem-owned. Account refresh re-issues a pair by copying the presented
// refresh token's tenant, credential, and auth-method claims forward. No
// account-session issuer sets those claims -- only direct-profile login does,
// and direct-profile refresh re-derives them from the database -- so on an
// account session they can only be stale or foreign. Copying them would
// launder them into a fresh access token that native routes treat as a tenant
// selection, so refresh refuses rather than forwarding or silently dropping
// them (dropping would turn a tenant-bound token into a default-organization
// session).
//
// The token must also name the session's account, and, when it carries one,
// the account's current incarnation: a token minted for an account since
// replaced under the same numeric ID must not refresh as the replacement.
func accountRefreshBindingHolds(claims *Claims, session *models.AuthSession, user *models.User) bool {
	if claims == nil || session == nil || user == nil {
		return false
	}
	if claims.UserID != session.UserID || user.ID != session.UserID {
		return false
	}
	if claims.AuthMethod == AuthMethodDirectProfile ||
		claims.OrganizationID != "" || claims.MembershipID != "" ||
		claims.PolicyRevision != 0 || claims.SecurityRevision != 0 ||
		claims.CredentialRevision != 0 {
		return false
	}
	if claims.AccountIncarnationID != "" && claims.AccountIncarnationID != user.AccountIncarnationID.String() {
		return false
	}
	return true
}

// bloemRefreshSession refreshes the session kinds Bloem adds, ahead of
// Silo's session checks: a direct-profile session revalidates its profile
// credential, and a preview guest session never slides. Any other session,
// and one that cannot be read or is no longer live, is left to Refresh,
// which answers for it.
func (s *Service) bloemRefreshSession(ctx context.Context, claims *Claims) (*TokenPair, bool, error) {
	session := s.bloemLiveSession(ctx, claims.SessionID)
	if session == nil {
		return nil, false, nil
	}
	switch session.AuthMethod {
	case AuthMethodDirectProfile:
		pair, err := s.refreshDirectProfile(ctx, claims, session)
		return pair, true, err
	case AuthMethodPreviewGuest:
		pair, err := s.refreshPreviewGuest(ctx, claims)
		return pair, true, err
	}
	return nil, false, nil
}

// bloemLiveSession is the session while it is live, and nil when it cannot be
// read or has ended.
func (s *Service) bloemLiveSession(ctx context.Context, id string) *models.AuthSession {
	session, err := s.sessions.GetByID(ctx, id)
	if err != nil || session.RevokedAt != nil || !session.ExpiresAt.After(time.Now()) {
		return nil
	}
	return session
}
