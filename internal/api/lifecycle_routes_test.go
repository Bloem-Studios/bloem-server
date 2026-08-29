package api

import (
	"net/http"
	"sort"
	"testing"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

func TestUnsafeLifecycleRouteRegistryHasStableUniqueContracts(t *testing.T) {
	contracts := LifecycleRouteContracts()
	if len(contracts) < 35 {
		t.Fatalf("registered lifecycle mutations = %d, want the reviewed cross-version surface", len(contracts))
	}
	seenIDs := make(map[string]bool, len(contracts))
	seenRoutes := make(map[string]bool, len(contracts))
	for _, contract := range contracts {
		if contract.ID == "" || contract.Pattern == "" || contract.Method == "" || !contract.Mutation {
			t.Fatalf("incomplete lifecycle contract: %+v", contract)
		}
		if seenIDs[contract.ID] {
			t.Fatalf("duplicate semantic route ID %q", contract.ID)
		}
		seenIDs[contract.ID] = true
		key := contract.Method + " " + contract.Pattern
		if seenRoutes[key] {
			t.Fatalf("duplicate lifecycle route %q", key)
		}
		seenRoutes[key] = true
	}

	for _, required := range []struct {
		method, pattern string
		source          lifecycleidempotency.TargetSource
	}{
		{http.MethodPost, "/api/v1/auth/setup", lifecycleidempotency.TargetBodyAccount},
		{http.MethodPost, "/api/v1/auth/signup", lifecycleidempotency.TargetBodyAccount},
		{http.MethodPost, "/api/v1/invitations/{token}/accept", lifecycleidempotency.TargetBodyAccount},
		{http.MethodDelete, "/api/v1/admin/users/{id}", lifecycleidempotency.TargetPathAccount},
		{http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}", lifecycleidempotency.TargetPathTenantMember},
		{http.MethodPatch, "/api/v2/admin/organization/people/{account_id}/memberships/current", lifecycleidempotency.TargetExactMembership},
		{http.MethodPost, "/api/v2/admin/organization/people/bulk-jobs", lifecycleidempotency.TargetStoredSelection},
		{http.MethodPost, "/api/v2/admin/platform/accounts/{account_id}/entitlement/apply", lifecycleidempotency.TargetPathAccount},
	} {
		contract, ok := LookupLifecycleRoute(required.method, required.pattern)
		if !ok {
			t.Errorf("missing lifecycle route %s %s", required.method, required.pattern)
			continue
		}
		if contract.TargetSource != required.source {
			t.Errorf("%s %s source = %q, want %q", required.method, required.pattern, contract.TargetSource, required.source)
		}
	}
}

func TestLifecycleOneShotExclusionsStayOutsideReplayRegistry(t *testing.T) {
	registered := make([]string, 0)
	for _, contract := range LifecycleRouteContracts() {
		registered = append(registered, contract.Method+" "+contract.Pattern)
	}
	sort.Strings(registered)
	for _, excluded := range LifecycleOneShotRoutes() {
		key := excluded.Method + " " + excluded.Pattern
		index := sort.SearchStrings(registered, key)
		if index < len(registered) && registered[index] == key {
			t.Errorf("one-shot route %q is also registered as replay-safe", key)
		}
	}
}

func TestLifecycleRouteMatcherClassifiesConcretePathsWithoutJellyfinFalsePositive(t *testing.T) {
	for _, test := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodDelete, "/api/v1/admin/users/42", true},
		{http.MethodDelete, "/api/v1/admin/tenants/bf64e282-8c30-4bcc-8166-9047e52cb623/members/42/auth-sessions/session-1", true},
		{http.MethodPost, "/api/v1/invitations/secret-token/accept", true},
		{http.MethodPatch, "/api/v2/admin/organization/people/42/memberships/current", true},
		{http.MethodPost, "/Users/42/PlayedItems/movie-1", false},
		{http.MethodPost, "/api/v1/auth/login", false},
		{http.MethodGet, "/api/v1/admin/users/42", false},
	} {
		_, got := MatchLifecycleRoute(test.method, test.path)
		if got != test.want {
			t.Errorf("MatchLifecycleRoute(%s %s) = %t, want %t", test.method, test.path, got, test.want)
		}
	}
}
