package tenancy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// MembershipPolicyExecer is the subset of a pgx transaction the writer marker
// needs, so callers can pass a pgx.Tx or a pool without this package caring.
type MembershipPolicyExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// MarkMembershipPolicyWriter marks the current transaction as the v1 membership
// policy writer.
//
// guard_membership_policy_write (20260829085838_membership_policy_isolation)
// rejects every write to a policy column on organization_memberships that does
// not carry this marker, so any statement touching access_group_id, permissions,
// library_ids, the playback limits, max_profiles or access_policy_revision has
// to call this first, in the same transaction.
//
// The setting is transaction-local: SET LOCAL semantics mean it cannot leak onto
// the next borrower of a pooled connection, which is exactly why the guard uses
// it as the marker rather than a session-level GUC.
func MarkMembershipPolicyWriter(ctx context.Context, tx MembershipPolicyExecer) error {
	if tx == nil {
		return fmt.Errorf("tenancy: marking the membership policy writer requires a transaction")
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer', 'v1', true)`); err != nil {
		return fmt.Errorf("tenancy: mark membership policy writer: %w", err)
	}
	return nil
}
