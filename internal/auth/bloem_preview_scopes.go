package auth

const ScopeAdminPreview = "admin:preview"

// Bloem's platform entitlement scopes. Each needs an enabled platform
// administrator as the owning account.
const (
	// ScopeAdminEntitlementsBulk permits the generic platform cohort and
	// confirmed durable bulk-policy surface. The owning account must still be
	// an enabled platform administrator.
	ScopeAdminEntitlementsBulk = "admin:entitlements:bulk"

	// ScopeAdminEntitlementsAccounts permits reading the entitlement template
	// catalog and previewing and applying a template to one account, which is
	// what a storefront needs to provision accounts on templates. The owning
	// account must still be an enabled platform administrator; creating,
	// revising, or archiving templates stays with an admin session.
	ScopeAdminEntitlementsAccounts = "admin:entitlements:accounts"
)

func bloemPreviewAPIKeyScope() APIKeyScope {
	return APIKeyScope{
		Name: ScopeAdminPreview, Audience: APIKeyScopeAudienceAdmin,
		Description: "Configure the read-only preview, its launch keys and curated catalog. Requires a Platform administrator.",
	}
}
