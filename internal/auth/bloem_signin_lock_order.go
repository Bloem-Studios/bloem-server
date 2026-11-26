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
