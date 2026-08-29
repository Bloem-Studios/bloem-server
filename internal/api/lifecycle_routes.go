package api

import (
	"net/http"
	"strings"
	"sync"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

type RouteContract struct {
	ID           string
	Method       string
	Pattern      string
	TargetSource lifecycleidempotency.TargetSource
	Mutation     bool
	Preauth      bool
}

type ReviewedOneShotRoute struct {
	Method  string
	Pattern string
	Reason  string
}

type ReviewedNonMutationRoute struct {
	Method  string
	Pattern string
	Reason  string
}

var lifecycleRouteContracts = []RouteContract{
	lifecycle("auth.setup", http.MethodPost, "/api/v1/auth/setup", lifecycleidempotency.TargetBodyAccount, true),
	lifecycle("auth.signup", http.MethodPost, "/api/v1/auth/signup", lifecycleidempotency.TargetBodyAccount, true),
	lifecycle("invitation.accept", http.MethodPost, "/api/v1/invitations/{token}/accept", lifecycleidempotency.TargetBodyAccount, true),
	lifecycle("profile.create", http.MethodPost, "/api/v1/profiles/", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("profile.update", http.MethodPut, "/api/v1/profiles/{id}", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("profile.delete", http.MethodDelete, "/api/v1/profiles/{id}", lifecycleidempotency.TargetExactMembership, false),

	lifecycle("tenant.create", http.MethodPost, "/api/v1/admin/tenants", lifecycleidempotency.TargetBodyAccount, false),
	lifecycle("tenant.limits.update", http.MethodPatch, "/api/v1/admin/tenants/{id}/limits", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("tenant.freeze", http.MethodPost, "/api/v1/admin/tenants/{id}/freeze", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("tenant.thaw", http.MethodPost, "/api/v1/admin/tenants/{id}/thaw", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("tenant.delete", http.MethodDelete, "/api/v1/admin/tenants/{id}", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("tenant.member.create", http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members", lifecycleidempotency.TargetBodyAccount, false),
	lifecycle("tenant.member.update", http.MethodPut, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.delete", http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.suspend", http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/suspend", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.resume", http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/resume", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.reset_password", http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/reset-password", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.profile.create", http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/profiles", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.profile.update", http.MethodPut, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.profile.delete", http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.device.delete", http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/devices/{device_id}", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.session.delete", http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/auth-sessions/{session_id}", lifecycleidempotency.TargetPathTenantMember, false),
	lifecycle("tenant.member.sessions.delete", http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/auth-sessions", lifecycleidempotency.TargetPathTenantMember, false),

	lifecycle("account.create", http.MethodPost, "/api/v1/admin/users", lifecycleidempotency.TargetBodyAccount, false),
	lifecycle("account.update", http.MethodPut, "/api/v1/admin/users/{id}", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.delete", http.MethodDelete, "/api/v1/admin/users/{id}", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.impersonate", http.MethodPost, "/api/v1/admin/users/{id}/impersonate", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.profile.create", http.MethodPost, "/api/v1/admin/users/{user_id}/profiles", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.profile.update", http.MethodPut, "/api/v1/admin/users/{user_id}/profiles/{profile_id}", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.profile.delete", http.MethodDelete, "/api/v1/admin/users/{user_id}/profiles/{profile_id}", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.device.delete", http.MethodDelete, "/api/v1/admin/users/{user_id}/devices/{device_id}", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.session.delete", http.MethodDelete, "/api/v1/admin/users/{user_id}/auth-sessions/{session_id}", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.sessions.delete", http.MethodDelete, "/api/v1/admin/users/{user_id}/auth-sessions", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.setting.set", http.MethodPut, "/api/v1/admin/users/{id}/settings/values/{key}", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.setting.delete", http.MethodDelete, "/api/v1/admin/users/{id}/settings/values/{key}", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("account.request_limit.update", http.MethodPut, "/api/v1/admin/request-users/{user_id}/limit", lifecycleidempotency.TargetPathAccount, false),

	lifecycle("organization.create", http.MethodPost, "/api/v2/admin/platform/organizations/", lifecycleidempotency.TargetBodyAccount, false),
	lifecycle("organization.update", http.MethodPatch, "/api/v2/admin/platform/organizations/{id}", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("organization.suspend", http.MethodPost, "/api/v2/admin/platform/organizations/{id}/suspend", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("organization.reactivate", http.MethodPost, "/api/v2/admin/platform/organizations/{id}/reactivate", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("organization.transfer", http.MethodPost, "/api/v2/admin/platform/organizations/{id}/transfer-ownership", lifecycleidempotency.TargetBodyAccount, false),
	lifecycle("organization.membership.create", http.MethodPost, "/api/v2/admin/platform/organizations/{id}/memberships", lifecycleidempotency.TargetBodyAccount, false),
	lifecycle("organization.membership.update", http.MethodPatch, "/api/v2/admin/platform/organizations/{id}/memberships/{membership_id}", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("people.membership.update", http.MethodPatch, "/api/v2/admin/organization/people/{account_id}/memberships/current", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("people.profile.update", http.MethodPatch, "/api/v2/admin/organization/people/{account_id}/profiles/{profile_id}", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("people.policy_job.create", http.MethodPost, "/api/v2/admin/organization/people/policy-jobs", lifecycleidempotency.TargetStoredSelection, false),
	lifecycle("people.bulk_job.create", http.MethodPost, "/api/v2/admin/organization/people/bulk-jobs", lifecycleidempotency.TargetStoredSelection, false),
	lifecycle("entitlement.organization.apply", http.MethodPost, "/api/v2/admin/platform/organizations/{id}/entitlement/apply", lifecycleidempotency.TargetExactMembership, false),
	lifecycle("entitlement.account.apply", http.MethodPost, "/api/v2/admin/platform/accounts/{account_id}/entitlement/apply", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("entitlement.account.apply_legacy", http.MethodPost, "/api/v2/admin/platform/users/{user_id}/entitlement/apply", lifecycleidempotency.TargetPathAccount, false),
	lifecycle("entitlement.organization.policy_job.create", http.MethodPost, "/api/v2/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs", lifecycleidempotency.TargetStoredSelection, false),
	lifecycle("entitlement.direct.policy_job.create", http.MethodPost, "/api/v2/admin/platform/accounts/entitlement-bulk/policy-jobs", lifecycleidempotency.TargetStoredSelection, false),
}

var lifecycleOneShotRoutes = []ReviewedOneShotRoute{
	{http.MethodPost, "/api/v1/auth/login", "credential exchange"},
	{http.MethodPost, "/api/v1/auth/profile-login", "credential exchange"},
	{http.MethodPost, "/api/v1/auth/refresh", "token rotation"},
	{http.MethodPost, "/api/v1/profiles/{id}/verify-pin", "credential verification"},
	{http.MethodPut, "/api/v1/profiles/{id}/avatar", "streaming upload"},
	{http.MethodDelete, "/api/v1/profiles/{id}/avatar", "one-shot media mutation"},
	{http.MethodPost, "/api/v1/auth/device/start", "device pairing"},
	{http.MethodPost, "/api/v1/auth/device/poll", "device pairing"},
	{http.MethodPost, "/api/v1/auth/plugin-launch", "short-lived credential issuance"},
	{http.MethodPost, "/api/v1/auth/oauth/init", "OAuth state issuance"},
	{http.MethodPost, "/api/v1/auth/oauth/complete", "OAuth callback"},
}

var lifecycleNonMutationRoutes = []ReviewedNonMutationRoute{
	{http.MethodPost, "/api/v2/admin/platform/accounts/{account_id}/entitlement/dry-run", "entitlement preview"},
	{http.MethodPost, "/api/v2/admin/platform/users/{user_id}/entitlement/dry-run", "legacy entitlement preview"},
	{http.MethodPost, "/api/v2/admin/platform/organizations/{id}/entitlement/dry-run", "organization entitlement preview"},
	{http.MethodPost, "/api/v2/admin/platform/accounts/{account_id}/entitlement", "method rejected by exact route dispatch"},
	{http.MethodPut, "/api/v2/admin/platform/accounts/{account_id}/entitlement", "method rejected by exact route dispatch"},
	{http.MethodPatch, "/api/v2/admin/platform/accounts/{account_id}/entitlement", "method rejected by exact route dispatch"},
	{http.MethodDelete, "/api/v2/admin/platform/accounts/{account_id}/entitlement", "method rejected by exact route dispatch"},
	{http.MethodPost, "/api/v2/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement", "method rejected by exact route dispatch"},
	{http.MethodPut, "/api/v2/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement", "method rejected by exact route dispatch"},
	{http.MethodPatch, "/api/v2/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement", "method rejected by exact route dispatch"},
	{http.MethodDelete, "/api/v2/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement", "method rejected by exact route dispatch"},
}

var (
	lifecycleRouteIndexOnce sync.Once
	lifecycleRouteIndex     map[string]RouteContract
)

func lifecycle(id, method, pattern string, source lifecycleidempotency.TargetSource, preauth bool) RouteContract {
	return RouteContract{ID: id, Method: method, Pattern: pattern, TargetSource: source, Mutation: true, Preauth: preauth}
}

func LifecycleRouteContracts() []RouteContract {
	return append([]RouteContract(nil), lifecycleRouteContracts...)
}

func LifecycleOneShotRoutes() []ReviewedOneShotRoute {
	return append([]ReviewedOneShotRoute(nil), lifecycleOneShotRoutes...)
}

func LifecycleNonMutationRoutes() []ReviewedNonMutationRoute {
	return append([]ReviewedNonMutationRoute(nil), lifecycleNonMutationRoutes...)
}

func LookupLifecycleRoute(method, pattern string) (RouteContract, bool) {
	lifecycleRouteIndexOnce.Do(func() {
		lifecycleRouteIndex = make(map[string]RouteContract, len(lifecycleRouteContracts))
		for _, contract := range lifecycleRouteContracts {
			lifecycleRouteIndex[contract.Method+" "+contract.Pattern] = contract
		}
	})
	contract, ok := lifecycleRouteIndex[method+" "+pattern]
	return contract, ok
}

func MatchLifecycleRoute(method, requestPath string) (RouteContract, bool) {
	for _, contract := range lifecycleRouteContracts {
		if contract.Method == method && lifecyclePathMatches(contract.Pattern, requestPath) {
			return contract, true
		}
	}
	return RouteContract{}, false
}

func lifecyclePathMatches(pattern, requestPath string) bool {
	pattern = strings.TrimSuffix(pattern, "/")
	requestPath = strings.TrimSuffix(requestPath, "/")
	patternParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	requestParts := strings.Split(strings.TrimPrefix(requestPath, "/"), "/")
	if len(patternParts) != len(requestParts) {
		return false
	}
	for index := range patternParts {
		part := patternParts[index]
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if requestParts[index] == "" {
				return false
			}
			continue
		}
		if part != requestParts[index] {
			return false
		}
	}
	return true
}
