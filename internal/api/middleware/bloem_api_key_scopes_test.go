package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// TestBloemEntitlementBulkAPIKeyScope covers Bloem's admin:entitlements:bulk
// scope; kept out of Silo's TestAPIKeyScopesAllow table.
func TestBloemEntitlementBulkAPIKeyScope(t *testing.T) {
	entitlementBulk := []string{auth.ScopeAdminEntitlementsBulk}
	tests := []struct {
		name   string
		scopes []string
		method string
		path   string
		want   bool
	}{
		{"entitlement bulk lists cohorts", entitlementBulk, http.MethodGet, "/api/bloem/v1/admin/platform/organizations/10000000-0000-0000-0000-000000000001/entitlement-cohorts", true},
		{"entitlement bulk gets cohort", entitlementBulk, http.MethodGet, "/api/bloem/v1/admin/platform/organizations/10000000-0000-0000-0000-000000000001/entitlement-cohorts/20000000-0000-0000-0000-000000000002", true},
		{"entitlement bulk previews organization", entitlementBulk, http.MethodPost, "/api/bloem/v1/admin/platform/organizations/10000000-0000-0000-0000-000000000001/entitlement-bulk/policy-previews", true},
		{"entitlement bulk gets organization job", entitlementBulk, http.MethodGet, "/api/bloem/v1/admin/platform/organizations/10000000-0000-0000-0000-000000000001/entitlement-bulk/policy-jobs/job-1", true},
		{"entitlement bulk cancels direct job", entitlementBulk, http.MethodPost, "/api/bloem/v1/admin/platform/accounts/entitlement-bulk/policy-jobs/job-1/cancel", true},
		{"entitlement bulk reads direct account policy", entitlementBulk, http.MethodGet, "/api/bloem/v1/admin/platform/accounts/42/entitlement", true},
		{"entitlement bulk reads organization account policy", entitlementBulk, http.MethodGet, "/api/bloem/v1/admin/platform/organizations/10000000-0000-0000-0000-000000000001/accounts/42/entitlement", true},
		{"entitlement bulk reads direct account snapshots", entitlementBulk, http.MethodPost, "/api/bloem/v1/admin/platform/accounts/entitlement-snapshots", true},
		{"entitlement bulk reads organization account snapshots", entitlementBulk, http.MethodPost, "/api/bloem/v1/admin/platform/organizations/10000000-0000-0000-0000-000000000001/entitlement-snapshots", true},
		{"entitlement bulk denies wrong method", entitlementBulk, http.MethodDelete, "/api/bloem/v1/admin/platform/organizations/10000000-0000-0000-0000-000000000001/entitlement-cohorts", false},
		{"entitlement bulk denies malformed organization id", entitlementBulk, http.MethodGet, "/api/bloem/v1/admin/platform/organizations/----/entitlement-cohorts", false},
		{"entitlement bulk denies account policy mutation", entitlementBulk, http.MethodPost, "/api/bloem/v1/admin/platform/accounts/42/entitlement/apply", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, nil)
			if got := apiKeyScopesAllow(tt.scopes, r); got != tt.want {
				t.Fatalf("apiKeyScopesAllow(%v, %s %s) = %v, want %v", tt.scopes, tt.method, tt.path, got, tt.want)
			}
		})
	}
}

// TestBloemEntitlementAccountsAPIKeyScope covers admin:entitlements:accounts:
// the template catalog read and per-account preview and apply, nothing else.
func TestBloemEntitlementAccountsAPIKeyScope(t *testing.T) {
	accounts := []string{auth.ScopeAdminEntitlementsAccounts}
	tests := []struct {
		name   string
		scopes []string
		method string
		path   string
		want   bool
	}{
		{"accounts scope lists templates", accounts, http.MethodGet, "/api/bloem/v1/admin/platform/entitlement-templates", true},
		{"accounts scope previews an account", accounts, http.MethodPost, "/api/bloem/v1/admin/platform/accounts/42/entitlement/dry-run", true},
		{"accounts scope applies to an account", accounts, http.MethodPost, "/api/bloem/v1/admin/platform/accounts/42/entitlement/apply", true},
		{"accounts scope denies template creation", accounts, http.MethodPost, "/api/bloem/v1/admin/platform/entitlement-templates", false},
		{"accounts scope denies template revision", accounts, http.MethodPost, "/api/bloem/v1/admin/platform/entitlement-templates/standard/revisions", false},
		{"accounts scope denies one template read", accounts, http.MethodGet, "/api/bloem/v1/admin/platform/entitlement-templates/standard", false},
		{"accounts scope denies organization apply", accounts, http.MethodPost, "/api/bloem/v1/admin/platform/organizations/10000000-0000-0000-0000-000000000001/entitlement/apply", false},
		{"accounts scope denies malformed account id", accounts, http.MethodPost, "/api/bloem/v1/admin/platform/accounts/abc/entitlement/apply", false},
		{"accounts scope denies bulk jobs", accounts, http.MethodPost, "/api/bloem/v1/admin/platform/accounts/entitlement-bulk/policy-jobs", false},
		{"bulk scope still denies account apply", []string{auth.ScopeAdminEntitlementsBulk}, http.MethodPost, "/api/bloem/v1/admin/platform/accounts/42/entitlement/dry-run", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, nil)
			if got := apiKeyScopesAllow(tt.scopes, r); got != tt.want {
				t.Fatalf("apiKeyScopesAllow(%v, %s %s) = %v, want %v", tt.scopes, tt.method, tt.path, got, tt.want)
			}
		})
	}
}
