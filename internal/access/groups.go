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
type GroupSubject = accesspolicy.GroupSubject
type GroupPolicyProvider = accesspolicy.GroupPolicyProvider
type GroupPolicy = accesspolicy.GroupPolicy
type EffectiveUserPolicy = accesspolicy.EffectiveUserPolicy

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

func NoGroupPolicy() GroupPolicy { return accesspolicy.NoGroupPolicy() }

// GroupApplies reports whether an access group contributes to the user's
// effective policy. Admin accounts are never capped by a group.
func GroupApplies(user *models.User) bool {
	return user != nil && user.AccessGroupID != nil && user.Role != models.RoleAdmin
}

// EffectivePolicyForSubject resolves the provider-selected group through the
// shared dependency-neutral evaluator.
func EffectivePolicyForSubject(ctx context.Context, user *models.User, subject GroupSubject, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	return accesspolicy.EffectivePolicyForSubject(ctx, user, subject, provider)
}

// EffectivePolicyForUser loads a user's group policy from the validated
// request tenant, preserving the legacy no-context fallback.
func EffectivePolicyForUser(ctx context.Context, user *models.User, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	if provider == nil || user == nil {
		return ApplyGroupPolicy(user, nil), nil
	}
	subject, err := GroupSubjectFromContext(ctx, user.ID, "")
	if err != nil {
		return ApplyGroupPolicy(user, nil), nil //nolint:nilerr // deliberate compatibility fallback
	}
	return EffectivePolicyForSubject(ctx, user, subject, provider)
}

func ApplyGroupPolicy(user *models.User, group *GroupPolicy) EffectiveUserPolicy {
	return accesspolicy.ApplyGroupPolicy(user, group)
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
