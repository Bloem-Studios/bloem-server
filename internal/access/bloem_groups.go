package access

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// Bloem's additions to Silo's group policy (groups.go). Groups belong to an
// organization, so a tenant-scoped provider resolves an account's group from
// the request's validated tenant instead of users.access_group_id; preview
// groups browse and nothing else; a group may switch playback off and cap the
// account's own profile limit. Silo's functions in groups.go call in here from
// added lines and otherwise run as Silo wrote them.

// PreviewTemplateKey is the reserved, protected entitlement template whose
// materialized groups make their accounts preview principals: they may browse
// the catalog and nothing else. The migration that seeds it also pins its
// policy with CHECK constraints, so the key alone is the preview signal.
const PreviewTemplateKey = "bloem-preview"

// GroupSubject identifies the tenant/account/profile whose group governs a request.
type GroupSubject struct {
	OrganizationID uuid.UUID
	AccountID      int
	ProfileID      string
	Legacy         bool
}

// SubjectPolicyProvider is Bloem's tenant-scoped extension of
// GroupPolicyProvider. A provider that implements it resolves the group for an
// organization, account and profile, and callers must supply a
// server-validated tenant subject.
type SubjectPolicyProvider interface {
	ResolvePolicy(context.Context, GroupSubject) (*GroupPolicy, error)
}

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

type policyProfileKey struct{}

// WithPolicyProfile names the household profile whose group EffectivePolicyForUser
// resolves for a tenant-scoped provider. Without it the subject is the account
// alone. Downloads set it: a profile's downloads follow that profile's group.
func WithPolicyProfile(ctx context.Context, profileID string) context.Context {
	return context.WithValue(ctx, policyProfileKey{}, profileID)
}

func policyProfile(ctx context.Context) string {
	profileID, _ := ctx.Value(policyProfileKey{}).(string)
	return profileID
}

// bloemTenantPolicyForUser answers EffectivePolicyForUser for a tenant-scoped
// provider, whose groups come from the account's organization membership.
// Any other provider is Silo's, and Silo's code in groups.go answers it.
func bloemTenantPolicyForUser(ctx context.Context, user *models.User, provider GroupPolicyProvider) (EffectiveUserPolicy, bool, error) {
	if user == nil {
		return EffectiveUserPolicy{}, false, nil
	}
	if _, scoped := provider.(SubjectPolicyProvider); !scoped {
		return EffectiveUserPolicy{}, false, nil
	}
	subject, err := GroupSubjectFromContext(ctx, user.ID, policyProfile(ctx))
	if err != nil {
		if tenantPolicyRequired(ctx) {
			return EffectiveUserPolicy{}, true, err
		}
		return ApplyGroupPolicy(user, nil), true, nil // no validated tenant: the compatibility fallback
	}
	policy, err := EffectivePolicyForSubject(ctx, user, subject, provider)
	return policy, true, err
}

// EffectivePolicyForSubject resolves the group the provider selects for the
// subject, then applies it with Silo's ApplyGroupPolicy. Admin accounts are
// never capped by a group, as in Silo.
func EffectivePolicyForSubject(ctx context.Context, user *models.User, subject GroupSubject, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	if provider == nil || user == nil || user.Role == models.RoleAdmin {
		return ApplyGroupPolicy(user, nil), nil
	}
	var group *GroupPolicy
	var err error
	if scoped, ok := provider.(SubjectPolicyProvider); ok {
		group, err = scoped.ResolvePolicy(ctx, subject)
	} else {
		group, err = provider.GetPolicyForUser(ctx, subject.AccountID)
	}
	if err != nil {
		return EffectiveUserPolicy{}, err
	}
	return ApplyGroupPolicy(user, group), nil
}

// EffectivePolicyForRequest resolves the account/profile's effective policy.
// A tenant-scoped provider (TenantGroupStore) requires the validated request
// tenant; Silo's account-level providers keep Silo's semantics and are only
// queried for an account whose group applies (GroupApplies).
func EffectivePolicyForRequest(ctx context.Context, user *models.User, profileID string, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	subject := GroupSubject{AccountID: user.ID, ProfileID: profileID}
	_, scoped := provider.(SubjectPolicyProvider)
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

// bloemPreviewOnlyPolicy is a preview group's resolved policy: the protected
// template's, whatever its account says. An override must not widen libraries
// or switch a capability back on.
func bloemPreviewOnlyPolicy(base GroupPolicy) EffectiveUserPolicy {
	var libraries []int
	if base.LibraryIDs != nil {
		libraries = sortedUniqueInts(base.LibraryIDs)
	}
	return EffectiveUserPolicy{
		LibraryIDs:         libraries,
		MaxPlaybackQuality: NormalizePlaybackQuality(base.MaxPlaybackQuality),
		MaxProfiles:        base.MaxProfiles,
		Permissions:        []string{},
		PreviewOnly:        true,
	}
}

// bloemApplyManagedProfileLimit caps the account's profile limit at a managed
// group's: the stricter positive limit wins, and a group limit of 0 means one.
func bloemApplyManagedProfileLimit(effective *EffectiveUserPolicy, group *GroupPolicy) {
	if group == nil || !group.ManagedProfileLimit {
		return
	}
	ceiling := group.MaxProfiles
	if ceiling == 0 {
		ceiling = 1
	}
	effective.MaxProfiles = strictestPositive(effective.MaxProfiles, ceiling)
}

func strictestPositive(account, group int) int {
	if account <= 0 {
		return group
	}
	if group <= 0 || account < group {
		return account
	}
	return group
}
