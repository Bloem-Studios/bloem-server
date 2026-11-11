package handlers

// Request and response types of the platform storefront routes
// (/api/bloem/v1/admin/platform/tenants and .../accounts/...),
// exported under these names only so their OpenAPI declaration
// (internal/apiv2/bloem_storefront_document.go) documents the exact types the
// handlers write.
type (
	BloemTenantWire           = tenantResponse
	BloemTenantMemberWire     = tenantMemberResponse
	BloemAdminAuthSessionWire = adminUserAuthSessionResponse
)
