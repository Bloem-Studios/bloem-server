package policy

import (
	"github.com/google/uuid"
)

func policyTenantIDs(facts TenantFacts) (uuid.UUID, uuid.UUID) {
	organizationID, _ := uuid.Parse(facts.OrganizationID)
	membershipID, _ := uuid.Parse(facts.MembershipID)
	return organizationID, membershipID
}
