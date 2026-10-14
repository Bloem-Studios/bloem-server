package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// previewAccountSQL is true when the account (aliased u) is a preview
// principal: not an admin, and an active membership group or any of its
// profiles' groups is materialized from the protected preview template.
// Read on every request, so a conversion takes effect on the next one.
const previewAccountSQL = `(u.role <> 'admin' AND (
	EXISTS (SELECT 1 FROM organization_memberships m
		JOIN access_groups g ON g.organization_id = m.organization_id AND g.id = m.access_group_id
		WHERE m.account_id = u.id AND m.status = 'active' AND g.managed_template_key = 'bloem-preview')
	OR EXISTS (SELECT 1 FROM user_profiles p
		JOIN access_groups g ON g.organization_id = p.organization_id AND g.id = p.access_group_id
		WHERE p.user_id = u.id AND g.managed_template_key = 'bloem-preview')))`

// ActiveSessionAuthority is ActiveSessionRole that also reports whether the
// session's account is a preview principal, from the same indexed lookup.
func (r *SessionRepository) ActiveSessionAuthority(ctx context.Context, id string) (role string, previewOnly, active bool, err error) {
	query := `SELECT CASE WHEN s.profile_id IS NOT NULL THEN 'user' ELSE u.role END, ` + previewAccountSQL + `
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.revoked_at IS NULL AND s.expires_at > NOW()`
	err = r.pool.QueryRow(ctx, query, id).Scan(&role, &previewOnly)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, fmt.Errorf("checking session validity: %w", err)
	}
	return role, previewOnly, true, nil
}

// AccountPreviewOnly reports whether an account is a preview principal. A
// missing account is not one.
func (r *SessionRepository) AccountPreviewOnly(ctx context.Context, accountID int) (bool, error) {
	var previewOnly bool
	err := r.pool.QueryRow(ctx, `SELECT `+previewAccountSQL+` FROM users u WHERE u.id = $1`, accountID).Scan(&previewOnly)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking preview account: %w", err)
	}
	return previewOnly, nil
}
