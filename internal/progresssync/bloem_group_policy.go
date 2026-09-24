package progresssync

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
)

// profileGroupPolicyApplies replaces Silo's access.GroupApplies gate in
// resolveSnapshot: Bloem access groups attach to the validated
// (organization, account, profile) subject, so the group is always resolved
// and ApplyGroupPolicy decides what applies.
func profileGroupPolicyApplies(*models.User) bool { return true }

// groupPolicyInTransaction resolves the group policy for the request's
// validated tenant subject inside the snapshot transaction.
func groupPolicyInTransaction(ctx context.Context, tx pgx.Tx, input access.ResolveInput) (*access.GroupPolicy, error) {
	subject, err := access.GroupSubjectFromContext(ctx, input.UserID, input.ProfileID)
	if err != nil {
		return nil, err
	}
	return access.GroupPolicyInTransaction(ctx, tx, subject)
}
