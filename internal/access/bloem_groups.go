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

type tenantPolicyRequiredKey struct{}

// RequireTenantPolicy marks ctx so that EffectivePolicyForUser, given a
// tenant-scoped provider, fails closed instead of falling back to the no-group
// policy when ctx carries no validated tenant for the account. Password login
// sets it: its request has no tenant, and the account it signs in must not be
// told it may do what its group denies.
func RequireTenantPolicy(ctx context.Context) context.Context {
	return context.WithValue(ctx, tenantPolicyRequiredKey{}, true)
}

func tenantPolicyRequired(ctx context.Context) bool {
	required, _ := ctx.Value(tenantPolicyRequiredKey{}).(bool)
	return required
}

// EffectivePolicyForSubject resolves the provider-selected group through the
// shared dependency-neutral evaluator.
func EffectivePolicyForSubject(ctx context.Context, user *models.User, subject GroupSubject, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	return accesspolicy.EffectivePolicyForSubject(ctx, user, subject, provider)
}

// EffectivePolicyForRequest resolves the account/profile's effective policy.
// A tenant-scoped provider (TenantGroupStore) requires the validated request
// tenant; Silo's account-level providers keep Silo's semantics and are only
// queried for an account whose group applies (GroupApplies).
func EffectivePolicyForRequest(ctx context.Context, user *models.User, profileID string, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	subject := GroupSubject{AccountID: user.ID, ProfileID: profileID}
	_, scoped := provider.(accesspolicy.SubjectPolicyProvider)
	if provider != nil && !scoped && !GroupApplies(user) {
		return ApplyGroupPolicy(user, nil), nil
	}
	if scoped {
		var err error
		subject, err = GroupSubjectFromContext(ctx, user.ID, profileID)
		if err != nil {
			return EffectiveUserPolicy{}, err
		}
	}
	return EffectivePolicyForSubject(ctx, user, subject, provider)
}
