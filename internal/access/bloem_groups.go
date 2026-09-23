package access

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// Dependency-neutral policy types are re-exported so existing access callers
// keep one stable API while persistence packages can use the same evaluator
// without importing request-tenancy wiring.
type GroupPolicyProvider = accesspolicy.GroupPolicyProvider
type GroupPolicy = accesspolicy.GroupPolicy
type EffectiveUserPolicy = accesspolicy.EffectiveUserPolicy

// GroupSubject identifies the tenant/account/profile whose group governs a request.
type GroupSubject = accesspolicy.GroupSubject

// GroupSubjectFromContext derives a group subject exclusively from a
// server-validated tenant context and the already-authenticated account/profile.
func GroupSubjectFromContext(ctx context.Context, accountID int, profileID string) (GroupSubject, error) {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || tenant.OrganizationID == uuid.Nil || tenant.AccountID != accountID {
		return GroupSubject{}, ErrGroupNotFound
	}
	return GroupSubject{
		OrganizationID: tenant.OrganizationID,
		AccountID:      accountID,
		ProfileID:      profileID,
		Legacy:         tenant.Legacy,
	}, nil
}

// EffectivePolicyForSubject resolves the provider-selected group through the
// shared dependency-neutral evaluator.
func EffectivePolicyForSubject(ctx context.Context, user *models.User, subject GroupSubject, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	return accesspolicy.EffectivePolicyForSubject(ctx, user, subject, provider)
}
