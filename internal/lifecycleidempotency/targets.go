package lifecycleidempotency

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ResolveAccountTargets locks an account and all of its memberships and
// returns their immutable identities in canonical order. Callers must invoke
// it only after receipt lookup has established that this is a first attempt.
func ResolveAccountTargets(ctx context.Context, tx pgx.Tx, accountID int) ([]TargetBinding, error) {
	rows, err := tx.Query(ctx, `
SELECT memberships.organization_id,memberships.id,users.id,users.account_incarnation_id
FROM public.users AS users
JOIN public.organization_memberships AS memberships ON memberships.account_id=users.id
WHERE users.id=$1
ORDER BY memberships.organization_id,memberships.id
FOR UPDATE OF users,memberships`, accountID)
	if err != nil {
		return nil, fmt.Errorf("resolve lifecycle account target: %w", err)
	}
	defer rows.Close()

	targets := make([]TargetBinding, 0, 1)
	for rows.Next() {
		var target TargetBinding
		if err := rows.Scan(&target.OrganizationID, &target.MembershipID, &target.AccountID, &target.AccountIncarnationID); err != nil {
			return nil, fmt.Errorf("scan lifecycle account target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lifecycle account targets: %w", err)
	}
	if len(targets) == 0 {
		return nil, ErrTargetNotFound
	}
	return targets, nil
}

// ResolveTenantMemberTarget locks a live organization, its selected
// membership, and the membership's account in canonical order. Both the
// membership id and account incarnation are retained so a replay can never
// attach to a replacement that happens to reuse the same numeric account id.
func ResolveTenantMemberTarget(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) (TargetBinding, error) {
	if organizationID == uuid.Nil || accountID <= 0 {
		return TargetBinding{}, ErrTargetNotFound
	}
	var lockedOrganization uuid.UUID
	if err := tx.QueryRow(ctx, `
SELECT id FROM public.organizations
WHERE id=$1 AND external_service_id IS NOT NULL
FOR UPDATE`, organizationID).Scan(&lockedOrganization); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TargetBinding{}, ErrTargetNotFound
		}
		return TargetBinding{}, fmt.Errorf("lock lifecycle tenant organization target: %w", err)
	}

	target := TargetBinding{OrganizationID: lockedOrganization, AccountID: accountID}
	if err := tx.QueryRow(ctx, `
SELECT id FROM public.organization_memberships
WHERE organization_id=$1 AND account_id=$2
FOR UPDATE`, organizationID, accountID).Scan(&target.MembershipID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TargetBinding{}, ErrTargetNotFound
		}
		return TargetBinding{}, fmt.Errorf("lock lifecycle tenant membership target: %w", err)
	}
	if err := tx.QueryRow(ctx, `
SELECT account_incarnation_id FROM public.users
WHERE id=$1
FOR UPDATE`, accountID).Scan(&target.AccountIncarnationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TargetBinding{}, ErrTargetNotFound
		}
		return TargetBinding{}, fmt.Errorf("lock lifecycle tenant member account target: %w", err)
	}
	return target, nil
}

// ResolveTenantOrganizationTargets locks a live tenant and captures the
// immutable identity of every current membership/account pair. An empty
// tenant cannot be represented by the v1 receipt target schema, so callers
// must retry after its first membership exists rather than write an
// ambiguously unbound receipt.
func ResolveTenantOrganizationTargets(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) ([]TargetBinding, error) {
	if organizationID == uuid.Nil {
		return nil, ErrTargetNotFound
	}
	var lockedOrganization uuid.UUID
	if err := tx.QueryRow(ctx, `
SELECT id FROM public.organizations
WHERE id=$1 AND external_service_id IS NOT NULL
FOR UPDATE`, organizationID).Scan(&lockedOrganization); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTargetNotFound
		}
		return nil, fmt.Errorf("lock lifecycle tenant organization target: %w", err)
	}
	rows, err := tx.Query(ctx, `
SELECT memberships.id,memberships.account_id,users.account_incarnation_id
FROM public.organization_memberships AS memberships
JOIN public.users AS users ON users.id=memberships.account_id
WHERE memberships.organization_id=$1
ORDER BY memberships.id,memberships.account_id
FOR UPDATE OF memberships,users`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("resolve lifecycle tenant organization targets: %w", err)
	}
	defer rows.Close()
	targets := make([]TargetBinding, 0, 1)
	for rows.Next() {
		target := TargetBinding{OrganizationID: lockedOrganization}
		if err := rows.Scan(&target.MembershipID, &target.AccountID, &target.AccountIncarnationID); err != nil {
			return nil, fmt.Errorf("scan lifecycle tenant organization target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lifecycle tenant organization targets: %w", err)
	}
	if len(targets) == 0 {
		return nil, ErrTargetUnavailable
	}
	return targets, nil
}

// ResolveProfileTarget locks one profile together with its exact membership
// and account incarnation. Profile ids are reusable strings, so retaining the
// membership and incarnation prevents a replay from attaching to a later
// replacement row with the same id.
func ResolveProfileTarget(ctx context.Context, tx pgx.Tx, accountID int, profileID string) (TargetBinding, error) {
	if accountID <= 0 || profileID == "" {
		return TargetBinding{}, ErrTargetNotFound
	}
	var target TargetBinding
	err := tx.QueryRow(ctx, `
SELECT profiles.organization_id,memberships.id,users.id,users.account_incarnation_id,profiles.id
FROM public.user_profiles AS profiles
JOIN public.users AS users ON users.id=profiles.user_id
JOIN public.organization_memberships AS memberships
  ON memberships.organization_id=profiles.organization_id AND memberships.account_id=users.id
WHERE profiles.user_id=$1 AND profiles.id=$2
FOR UPDATE OF memberships,users,profiles`, accountID, profileID).Scan(
		&target.OrganizationID, &target.MembershipID, &target.AccountID, &target.AccountIncarnationID, &target.ProfileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TargetBinding{}, ErrTargetNotFound
	}
	if err != nil {
		return TargetBinding{}, fmt.Errorf("resolve lifecycle profile target: %w", err)
	}
	return target, nil
}
