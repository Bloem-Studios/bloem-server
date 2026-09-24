package access

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/models"
)

func NoGroupPolicy() GroupPolicy { return accesspolicy.NoGroupPolicy() }

// GroupApplies reports whether an access group contributes to the user's
// effective policy. Admin accounts are never capped by a group: the repository
// keeps them ungrouped, and a row that still carries a group (written before
// that rule existed) is resolved as if it did not.
func GroupApplies(user *models.User) bool {
	return user != nil && user.AccessGroupID != nil && user.Role != models.RoleAdmin
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

// ApplyGroupPolicy resolves the user's account policy against the optional
// access group: each field takes the user's explicit override when set and
// the group's value otherwise. A nil group means the permissive
// NoGroupPolicy. Permissions are the one mask-style field: the group's
// allowed_permissions (when set) intersects the user's permissions.
func ApplyGroupPolicy(user *models.User, group *GroupPolicy) EffectiveUserPolicy {
	return accesspolicy.ApplyGroupPolicy(user, group)
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
