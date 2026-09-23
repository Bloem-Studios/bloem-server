package api

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// bloemOrgRevocationViewer refuses a profile selection that an organization
// revoked after the session was created.
//
// An organization-scoped session revoke bumps the membership's
// security_revision, which stales every token carrying that revision. An
// account-login session (auth_sessions.profile_id IS NULL) carries none, and
// when the account belongs to another organization as well it is deliberately
// left alive — one tenant's administrator must not sign the member out of
// another tenant. Such a session picks its profile per request with
// X-Profile-Id, so without this check it could keep selecting the revoking
// organization's profiles. The revocation time per (account, organization) is
// recorded by the migration 20260923160000_bloem_org_session_revocations.
//
// A refused profile answers access.ErrProfileNotFound, the same as a profile
// the session cannot see. Only sessions are checked: an API key has no
// auth_sessions row, and a direct-profile session is bound to its profile and
// revision already.
type bloemOrgRevocationViewer struct {
	inner apimw.ViewerResolver
	pool  *pgxpool.Pool
}

// bloemOrgRevocationAwareViewer decorates the viewer resolver the legacy
// viewer-access middleware uses. Without a database there is nothing to check.
func bloemOrgRevocationAwareViewer(inner apimw.ViewerResolver, pool *pgxpool.Pool) apimw.ViewerResolver {
	if inner == nil || pool == nil {
		return inner
	}
	return bloemOrgRevocationViewer{inner: inner, pool: pool}
}

func (v bloemOrgRevocationViewer) Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error) {
	if input.ProfileID != "" && input.SessionID != "" && input.UserID > 0 {
		revoked, err := sessionProfileOrganizationRevoked(ctx, v.pool, input.SessionID, input.UserID, input.ProfileID)
		if err != nil {
			return access.Scope{}, err
		}
		if revoked {
			return access.Scope{}, access.ErrProfileNotFound
		}
	}
	return v.inner.Resolve(ctx, input)
}

// sessionProfileOrganizationRevoked reports whether the account-login session
// was created before the profile's organization last revoked the account.
func sessionProfileOrganizationRevoked(ctx context.Context, pool *pgxpool.Pool, sessionID string, accountID int, profileID string) (bool, error) {
	var revoked bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM auth_sessions AS sessions
			JOIN user_profiles AS profiles
			  ON profiles.user_id = sessions.user_id AND profiles.id = $3
			JOIN bloem_org_session_revocations AS revocations
			  ON revocations.account_id = sessions.user_id
			 AND revocations.organization_id = profiles.organization_id
			WHERE sessions.id = $1
			  AND sessions.user_id = $2
			  AND sessions.profile_id IS NULL
			  AND sessions.created_at < revocations.revoked_at
		)`, sessionID, accountID, profileID).Scan(&revoked)
	if err != nil {
		return false, fmt.Errorf("check organization session revocation: %w", err)
	}
	return revoked, nil
}
