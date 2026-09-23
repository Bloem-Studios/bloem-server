package handlers

// Bloem-owned. What "revoke all sessions" means when an organization
// administrator asks for it.
//
// Revoking only the sessions bound to the organization's profiles left two
// ways back in. Anything bound to the membership itself -- admin-context
// tokens, tenant-bound (v2) sessions, direct-profile refresh -- carried the
// membership's security_revision and kept validating against it. And an
// account-login session (profile_id NULL) is not bound to any organization at
// all, so it survived and could still select the organization's profiles.
//
// Here the membership's security_revision moves, which makes every
// membership-bound token stale at its next check, and when this organization
// is the account's only active one the account-login sessions are revoked as
// well: they can reach nothing else. An account that still belongs to another
// organization keeps its account-login sessions, because one tenant's
// administrator must not sign the member out of another tenant.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// lockOrganizationMembership takes the membership row ahead of the profiles and
// sessions a revocation touches, matching the tenancy writers' lock order
// (membership before profile).
func lockOrganizationMembership(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) error {
	var membershipID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM organization_memberships
		WHERE organization_id = $1 AND account_id = $2
		FOR NO KEY UPDATE`, organizationID, accountID).Scan(&membershipID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lock organization membership: %w", err)
	}
	return nil
}

// revokeOrganizationMemberAccess invalidates the member's standing in one
// organization inside the caller's transaction. It reports whether the
// account's sessions were revoked account-wide, in which case the caller owes
// an account-wide compatibility-session invalidation after commit.
func revokeOrganizationMemberAccess(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) (bool, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE organization_memberships
		SET security_revision = security_revision + 1, updated_at = now()
		WHERE organization_id = $1 AND account_id = $2`, organizationID, accountID); err != nil {
		return false, fmt.Errorf("bump membership security revision: %w", err)
	}
	var otherMembership bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM organization_memberships AS memberships
			JOIN organizations AS orgs ON orgs.id = memberships.organization_id
			WHERE memberships.account_id = $1
			  AND memberships.organization_id <> $2
			  AND memberships.status = 'active'
			  AND orgs.status <> 'suspended'
		)`, accountID, organizationID).Scan(&otherMembership); err != nil {
		return false, fmt.Errorf("check other memberships: %w", err)
	}
	if otherMembership {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL`, accountID); err != nil {
		return false, fmt.Errorf("revoke account sessions: %w", err)
	}
	return true, nil
}

// revokeOrganizationMemberAccessStandalone runs revokeOrganizationMemberAccess
// in its own transaction, for the path without lifecycle idempotency. A
// handler without a database pool has no memberships to act on (only test
// wiring builds one), so there is nothing to do.
func (h *AdminHandler) revokeOrganizationMemberAccessStandalone(ctx context.Context, organizationID uuid.UUID, accountID int) (bool, error) {
	if h.pool == nil {
		return false, nil
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin organization member revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockOrganizationMembership(ctx, tx, organizationID, accountID); err != nil {
		return false, err
	}
	accountWide, err := revokeOrganizationMemberAccess(ctx, tx, organizationID, accountID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit organization member revocation: %w", err)
	}
	return accountWide, nil
}
