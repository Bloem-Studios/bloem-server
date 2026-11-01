package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// Bloem-owned. The OpenAPI declarations of an organization's domains and
// branding routes (internal/api/handlers/bloem_organization_domains.go),
// advertised by the organization_domains_v1 capability.

type BloemTenantDomainsPathInput struct {
	ID string `path:"id" doc:"The organization ID (UUID). A malformed or unknown ID is not_found."`
}

type BloemTenantDomainInput struct {
	ID   string `path:"id" doc:"The organization ID (UUID)."`
	Host string `path:"host" doc:"A host registered to the organization, such as main.example.com." example:"main.example.com"`
}

type BloemTenantDomainCreateInput struct {
	ID   string `path:"id" doc:"The organization ID (UUID)."`
	Body handlers.BloemOrganizationDomainRequest
}

type BloemTenantDomainsOutput struct {
	Body handlers.BloemOrganizationDomainsResponse
}

type BloemTenantDomainOutput struct {
	Body handlers.BloemOrganizationDomainResponse
}

type BloemTenantBrandingOutput struct {
	Body handlers.BloemOrganizationBranding
}

type BloemTenantBrandingUpdateInput struct {
	ID   string `path:"id" doc:"The organization ID (UUID)."`
	Body handlers.BloemOrganizationBrandingUpdate
}

type BloemTenantBrandingAssetInput struct {
	ID      string `path:"id" doc:"The organization ID (UUID)."`
	Kind    string `path:"kind" enum:"wordmark,mark,favicon,login_bg" doc:"Branding asset slot" example:"mark"`
	RawBody huma.MultipartFormFiles[AdminBrandingAssetForm]
}

type BloemTenantBrandingAssetPathInput struct {
	ID   string `path:"id" doc:"The organization ID (UUID)."`
	Kind string `path:"kind" enum:"wordmark,mark,favicon,login_bg" doc:"Branding asset slot" example:"mark"`
}

type BloemTenantBrandingAssetOutput struct {
	Body handlers.BloemOrganizationBrandingAssetResponse
}

func registerBloemOrganizationDomainsDocument(reg *Registry) {
	notFound := "not_found: no such organization (a malformed ID answers the same)."
	defaultOrg := "default_organization: the default organization uses the server's branding settings."

	list := bloemStorefrontOp(reg, "GET", "/tenants/{id}/domains", "listBloemPlatformTenantDomains", "List an organization's domains.", http.StatusOK, map[int]string{404: notFound})
	list.Description += "The primary domain comes first. A request on a registered domain shows its organization's branding, and that organization's emails link to its primary domain. A domain never decides what an account can reach."
	registerBloemChiDocument[BloemTenantDomainsPathInput, BloemTenantDomainsOutput](reg, list)

	add := bloemStorefrontOp(reg, "POST", "/tenants/{id}/domains", "addBloemPlatformTenantDomain", "Register a domain to an organization.", http.StatusCreated, map[int]string{
		400: "invalid_json: the body is not valid JSON.",
		404: notFound,
		409: "domain_taken: the host is already registered.",
		422: "invalid_host: a host is a lower-case name with at least one dot, without scheme, port or path.",
	})
	add.Description += "An organization's first domain is its primary; primary on a later one moves the primary to it. An administrator's unscoped API key records the domain as the storefront's (source garden), a Platform context as the platform administrator's."
	registerBloemChiDocument[BloemTenantDomainCreateInput, BloemTenantDomainOutput](reg, add)

	primary := bloemStorefrontOp(reg, "PUT", "/tenants/{id}/domains/{host}/primary", "setBloemPlatformTenantPrimaryDomain", "Make a domain the organization's primary.", http.StatusNoContent, map[int]string{404: "not_found: no such organization, or it has no such domain."})
	registerBloemChiDocument[BloemTenantDomainInput, struct{}](reg, primary)

	remove := bloemStorefrontOp(reg, "DELETE", "/tenants/{id}/domains/{host}", "removeBloemPlatformTenantDomain", "Unregister a domain.", http.StatusNoContent, map[int]string{404: "not_found: no such organization, or it has no such domain."})
	remove.Description += "Removing the primary makes the oldest remaining domain primary."
	registerBloemChiDocument[BloemTenantDomainInput, struct{}](reg, remove)

	get := bloemStorefrontOp(reg, "GET", "/tenants/{id}/branding", "getBloemPlatformTenantBranding", "Read an organization's branding.", http.StatusOK, map[int]string{404: notFound, 409: defaultOrg})
	get.Description += "Only the organization's own values: an empty field is Bloem's built-in default on its domains. assets maps each uploaded asset kind to its reference."
	registerBloemChiDocument[BloemTenantDomainsPathInput, BloemTenantBrandingOutput](reg, get)

	update := bloemStorefrontOp(reg, "PUT", "/tenants/{id}/branding", "updateBloemPlatformTenantBranding", "Change an organization's branding.", http.StatusOK, map[int]string{
		400: "invalid_json: the body is not valid JSON.",
		404: notFound,
		409: defaultOrg,
		422: "validation: server_name is at most 80 characters and login_subtitle at most 200, without control characters; accent_color is a colour such as #336699; default_theme a theme name such as dark.",
	})
	update.Description += "Changes the fields it names; an empty string clears one."
	registerBloemChiDocument[BloemTenantBrandingUpdateInput, BloemTenantBrandingOutput](reg, update)

	upload := bloemStorefrontOp(reg, "PUT", "/tenants/{id}/branding/assets/{kind}", "uploadBloemPlatformTenantBrandingAsset", "Upload an organization's branding image.", http.StatusOK, map[int]string{
		400: "bad_request: an unknown kind, a malformed form, or an unsupported image.",
		404: notFound,
		409: defaultOrg,
		413: "too_large: the image exceeds the kind's limit.",
		503: "unavailable: asset storage is not configured.",
	})
	upload.Description += "The same limits and processing as the server's own branding upload. The image is shown on the organization's domains only."
	registerBloemChiDocument[BloemTenantBrandingAssetInput, BloemTenantBrandingAssetOutput](reg, upload)

	clear := bloemStorefrontOp(reg, "DELETE", "/tenants/{id}/branding/assets/{kind}", "deleteBloemPlatformTenantBrandingAsset", "Remove an organization's branding image.", http.StatusNoContent, map[int]string{
		400: "bad_request: an unknown kind.",
		404: notFound,
		409: defaultOrg,
	})
	registerBloemChiDocument[BloemTenantBrandingAssetPathInput, struct{}](reg, clear)
}
