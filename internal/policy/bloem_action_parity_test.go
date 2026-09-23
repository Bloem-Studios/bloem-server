package policy_test

// Bloem helpers for Silo's action_parity_test.go.

import (
	"github.com/Silo-Server/silo-server/internal/policy"
)

func validLegacyTenantFacts() policy.TenantFacts {
	return policy.TenantFacts{
		Present:                    true,
		Legacy:                     true,
		OrganizationID:             "10000000-0000-0000-0000-000000000001",
		MembershipID:               "20000000-0000-0000-0000-000000000001",
		OrganizationStatus:         "initializing",
		MembershipStatus:           "active",
		OrganizationPolicyRevision: 7,
		MembershipSecurityRevision: 11,
	}
}
