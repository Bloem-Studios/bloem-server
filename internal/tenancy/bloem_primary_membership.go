package tenancy

// An account may hold several memberships. Everything that asks "which
// organization owns this account?" must answer identically, or a guard that
// checks one account at a time and a query that bounds a whole result set will
// disagree for exactly the accounts where it matters most.
//
// AccountOrganization answers it for one account, using an indexed lookup with
// LIMIT 1 because it sits on request-handling paths. PrimaryMembershipSQL
// answers it for every account at once, for callers that need to bound a set
// inside a single statement.
//
// The two are kept in step by TestPrimaryMembershipSQLAgreesWithAccountOrganization,
// which seeds an account holding several memberships and asserts both reach the
// same organization. Change one selection rule and change the other.

// PrimaryMembershipSQL projects (account_id, organization_id) with one row per
// account: the organization AccountOrganization would return for it.
//
// It takes no parameters, so callers may embed it as a subquery and filter on
// either column. The ORDER BY must match AccountOrganization's exactly.
const PrimaryMembershipSQL = `
	SELECT DISTINCT ON (memberships.account_id)
	       memberships.account_id,
	       memberships.organization_id
	FROM organization_memberships AS memberships
	JOIN organizations AS orgs ON orgs.id = memberships.organization_id
	WHERE memberships.status <> 'invited'
	ORDER BY memberships.account_id,
	         orgs.is_default DESC,
	         memberships.created_at ASC,
	         memberships.id ASC`
