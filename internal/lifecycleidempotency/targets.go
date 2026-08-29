package lifecycleidempotency

import (
	"context"
	"fmt"

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
