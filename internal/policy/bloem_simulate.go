package policy

func representativeTenantFacts() TenantFacts {
	return TenantFacts{
		Present:                    true,
		Legacy:                     true,
		OrganizationID:             "10000000-0000-0000-0000-000000000001",
		MembershipID:               "20000000-0000-0000-0000-000000000001",
		OrganizationStatus:         "initializing",
		MembershipStatus:           "active",
		OrganizationPolicyRevision: 1,
		MembershipSecurityRevision: 1,
	}
}

// withBloemGuardTenant gives the eval-cost guard's canned input a resolved
// tenant, so a custom policy is timed on the tenant-bounded path every Bloem
// request takes rather than on Silo's tenant-free one.
func withBloemGuardTenant(input any) any {
	switch in := input.(type) {
	case ScopeInput:
		in.Tenant = representativeTenantFacts()
		in.TenantLibraryIDs = []int{1, 2, 3, 4}
		return in
	case PermissionInput:
		in.Tenant = representativeTenantFacts()
		return in
	case ActionInput:
		in.Tenant = representativeTenantFacts()
		return in
	}
	return input
}
