package api

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// bloemOrgRevocationViewer refuses a profile selection that an organization
// revoked after the session was created.
//
// An organization-scoped session revoke bumps the membership's
// security_revision, which stales every token carrying that revision. An
// account-login session (auth_sessions.profile_id IS NULL) carries none and
// picks its profile per request with X-Profile-Id, so without this check it
// could keep selecting the revoking organization's profiles. The trigger from
// migration 20260923160000_bloem_org_session_revocations records the
// revocation time per (account, organization). It records one for every bump
// that leaves status and role unchanged, not only for an administrator's
// "revoke all sessions", which also ends the account's sessions itself.
// Initial ownership activation and some access edits record one as well.
//
// A refused session is ended, and the refusal is
// apimw.ErrSessionRevokedByOrganization. An account belongs to one
// organization (organization_memberships_one_per_account), so the session can
// select none of its profiles again. Ending it makes its next request and its
// refresh fail like any revoked session, and the client signs in again. The
// viewer-access gate answers the refusal itself with 401 session_invalid.
// Before this, it answered 404 "Profile not found", and clients kept
// refreshing a session that could never load a profile. The error wraps
// access.ErrProfileNotFound, so other callers still refuse the profile. Only
// sessions are checked: an API key has no auth_sessions row, and a
// direct-profile session is already bound to its profile and revision.
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
			if err := endRevokedSession(ctx, v.pool, input.SessionID, input.UserID); err != nil {
				return access.Scope{}, err
			}
			return access.Scope{}, apimw.ErrSessionRevokedByOrganization
		}
	}
	scope, err := v.inner.Resolve(ctx, input)
	if err != nil {
		return access.Scope{}, err
	}
	return narrowBloemPreviewScope(ctx, v.pool, scope)
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

// endRevokedSession ends a session that its profile's organization revoked.
// A session another request already ended is left as it is, and the caller
// still refuses it.
func endRevokedSession(ctx context.Context, pool *pgxpool.Pool, sessionID string, accountID int) error {
	tag, err := pool.Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, sessionID, accountID)
	if err != nil {
		return fmt.Errorf("end session revoked by organization: %w", err)
	}
	if tag.RowsAffected() > 0 {
		slog.InfoContext(ctx, "ended a session that started before its organization revoked the account",
			"user_id", accountID, "session_id", sessionID)
	}
	return nil
}
