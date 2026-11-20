package middleware

import (
	"net/http"
	"regexp"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// bloemOrganizationIDPattern matches an organization ID in Bloem's entitlement
// scope routes.
const bloemOrganizationIDPattern = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

// Exact admin routes only: this scope grants neither a guest session nor
// ordinary user, entitlement, library or server-settings administration.
func bloemPreviewScopeRoutes() []scopeRoute {
	return []scopeRoute{
		{http.MethodPut, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/accounts/[1-9][0-9]*/presentation$`)},
		{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/account$`)},
		{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/settings$`)},
		{http.MethodPut, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/settings$`)},
		{http.MethodPut, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/libraries$`)},
		{http.MethodPut, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/keys$`)},
		{http.MethodDelete, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/sessions$`)},
		{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/verify$`)},
		{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/capacity$`)},
		{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/preview/catalog$`)},
	}
}

// init adds Bloem's admin API key scopes to Silo's scope route table, so
// api_key_scopes.go stays as Silo ships it. Package variables are initialized
// before any init function runs, so the map exists here.
func init() {
	for scope, routes := range map[string][]scopeRoute{
		auth.ScopeAdminEntitlementsBulk: {
			{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/organizations/` + bloemOrganizationIDPattern + `/entitlement-cohorts$`)},
			{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/organizations/` + bloemOrganizationIDPattern + `/entitlement-cohorts/` + bloemOrganizationIDPattern + `$`)},
			{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/organizations/` + bloemOrganizationIDPattern + `/accounts/[0-9]+/entitlement$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/organizations/` + bloemOrganizationIDPattern + `/entitlement-snapshots$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/organizations/` + bloemOrganizationIDPattern + `/entitlement-bulk/policy-previews$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/organizations/` + bloemOrganizationIDPattern + `/entitlement-bulk/policy-jobs$`)},
			{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/organizations/` + bloemOrganizationIDPattern + `/entitlement-bulk/policy-jobs/[^/]+$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/organizations/` + bloemOrganizationIDPattern + `/entitlement-bulk/policy-jobs/[^/]+/cancel$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-previews$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-jobs$`)},
			{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-jobs/[^/]+$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-jobs/[^/]+/cancel$`)},
			{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/accounts/[0-9]+/entitlement$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/accounts/entitlement-snapshots$`)},
		},
		auth.ScopeAdminPreview: bloemPreviewScopeRoutes(),
		auth.ScopeAdminEntitlementsAccounts: {
			{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/entitlement-templates$`)},
			{http.MethodGet, regexp.MustCompile(`^/api/bloem/v2/admin/platform/entitlement-templates/[^/]+/revisions$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/accounts/[0-9]+/entitlement/dry-run$`)},
			{http.MethodPost, regexp.MustCompile(`^/api/bloem/v2/admin/platform/accounts/[0-9]+/entitlement/apply$`)},
		},
	} {
		apiKeyScopeRoutes[scope] = routes
	}
}
