package auth

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// BloemLockIdentitiesBeforeSessions takes the account's provider identities
// before a Bloem transaction writes several of the account's login sessions,
// in the order a plugin uninstall takes them (lockIdentitiesBeforeSessions):
// the uninstall then waits at the identities, or the transaction waits for it.
func BloemLockIdentitiesBeforeSessions(ctx context.Context, tx pgx.Tx, userID int) error {
	return lockIdentitiesBeforeSessions(ctx, tx, userID, false)
}

// BloemWithdrawDeviceSignIns is the start of Silo's account-wide revocation
// (revokeSignInsInTransaction) for a Bloem transaction that then revokes the
// account's login sessions itself: it takes the account's identities, then
// withdraws the device sign-ins the account approved that no device has
// collected, with Silo's own statement
// (withdrawDeviceSignInApprovalsInTransaction). Without it a device approved
// before the revocation could still collect a fresh session after it. The
// caller holds the account row, which approving and collecting a device
// sign-in lock first, so neither can slip in before the sessions end. It
// reports how many approvals it withdrew.
func BloemWithdrawDeviceSignIns(ctx context.Context, tx pgx.Tx, userID int) (int64, error) {
	if err := lockIdentitiesBeforeSessions(ctx, tx, userID, false); err != nil {
		return 0, err
	}
	return withdrawDeviceSignInApprovalsInTransaction(ctx, tx, []int{userID})
}
