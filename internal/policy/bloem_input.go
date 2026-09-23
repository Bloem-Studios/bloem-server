package policy

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// TenantFacts is the resolved tenant identity supplied to policy by trusted
// server adapters. Present distinguishes an authoritative resolved document
// from a zero-valued or caller-constructed input.
type TenantFacts struct {
	Present                    bool   `json:"present"`
	Legacy                     bool   `json:"legacy"`
	OrganizationID             string `json:"organization_id"`
	MembershipID               string `json:"membership_id"`
	OrganizationStatus         string `json:"organization_status"`
	MembershipStatus           string `json:"membership_status"`
	OrganizationPolicyRevision int64  `json:"organization_policy_revision"`
	MembershipSecurityRevision int64  `json:"membership_security_revision"`
}

// TenantFactsFromContext converts the server-resolved tenant context for the
// expected policy subject into the policy input contract. Missing, incomplete,
// inactive, or subject-mismatched tenant state is rejected before evaluation.
func TenantFactsFromContext(ctx context.Context, expectedAccountID int) (TenantFacts, error) {
	if ctx == nil {
		return TenantFacts{}, ErrTenantFactsUnavailable
	}
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || expectedAccountID <= 0 || tenant.AccountID != expectedAccountID ||
		tenant.OrganizationID == uuid.Nil || tenant.MembershipID == uuid.Nil ||
		tenant.PolicyRevision <= 0 || tenant.SecurityRevision <= 0 ||
		tenant.MembershipStatus != tenancy.MembershipActive ||
		(tenant.OrganizationStatus != tenancy.OrganizationActive &&
			(!tenant.Legacy || !tenant.OrganizationDefault || tenant.OrganizationStatus != tenancy.OrganizationInitializing)) {
		return TenantFacts{}, fmt.Errorf("%w: resolved tenant context is incomplete or inactive", ErrTenantFactsUnavailable)
	}
	return TenantFacts{
		Present:                    true,
		Legacy:                     tenant.Legacy,
		OrganizationID:             tenant.OrganizationID.String(),
		MembershipID:               tenant.MembershipID.String(),
		OrganizationStatus:         string(tenant.OrganizationStatus),
		MembershipStatus:           string(tenant.MembershipStatus),
		OrganizationPolicyRevision: tenant.PolicyRevision,
		MembershipSecurityRevision: tenant.SecurityRevision,
	}, nil
}
