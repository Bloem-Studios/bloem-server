package api

import (
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

func TestLifecycleRouteDigestCanonicalizesEveryContractField(t *testing.T) {
	contracts := []RouteContract{
		{ID: "account.delete", Method: http.MethodDelete, Pattern: "/api/v1/admin/users/{id}", TargetSource: lifecycleidempotency.TargetPathAccount, Mutation: true},
		{ID: "auth.signup", Method: http.MethodPost, Pattern: "/api/v1/auth/signup", TargetSource: lifecycleidempotency.TargetBodyAccount, Mutation: true, Preauth: true},
	}
	digest := digestLifecycleRouteContracts(contracts)
	const want = "9a6b53f42ad452171375d8df7be07b22818204c0c6813e7cd142e499f8d1aee4"
	if got := hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("canonical route digest = %s, want %s", got, want)
	}

	reversed := []RouteContract{contracts[1], contracts[0]}
	if got := digestLifecycleRouteContracts(reversed); got != digest {
		t.Fatalf("route digest depends on registration order: %x != %x", got, digest)
	}
	changed := append([]RouteContract(nil), contracts...)
	changed[0].TargetSource = lifecycleidempotency.TargetExactMembership
	if got := digestLifecycleRouteContracts(changed); got == digest {
		t.Fatal("route digest ignored a target-source change")
	}
}

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
		{http.MethodPatch, "/api/v1/admin/tenants/{id}/limits", lifecycleidempotency.TargetExactMembership},
		{http.MethodPatch, "/api/v2/admin/platform/organizations/{id}", lifecycleidempotency.TargetExactMembership},
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

func TestUnsafeLifecycleRouteContractMatchesProductionRouter(t *testing.T) {
	mounted := make(map[string]bool)
	for _, pair := range walkRoutes(t, newRouteInventoryRouter(t)) {
		mounted[normalizeLifecycleRoutePair(pair)] = true
	}
	registered := make(map[string]bool)
	for _, contract := range LifecycleRouteContracts() {
		pair := contract.Method + " " + contract.Pattern
		registered[normalizeLifecycleRoutePair(pair)] = true
		if !mounted[normalizeLifecycleRoutePair(pair)] {
			t.Errorf("lifecycle registry contains stale or conditionally unmounted route %q", pair)
		}
	}
	excluded := make(map[string]bool)
	for _, route := range LifecycleOneShotRoutes() {
		excluded[normalizeLifecycleRoutePair(route.Method+" "+route.Pattern)] = true
	}
	for _, route := range LifecycleNonMutationRoutes() {
		pair := normalizeLifecycleRoutePair(route.Method + " " + route.Pattern)
		excluded[pair] = true
		if !mounted[pair] {
			t.Errorf("reviewed nonmutation route is stale: %s", pair)
		}
	}

	for pair := range mounted {
		method, pattern := splitRoute(pair)
		if !unsafeLifecycleCandidate(method, pattern) {
			continue
		}
		if !registered[pair] && !excluded[pair] {
			t.Errorf("unsafe lifecycle candidate is unclassified: %s", pair)
		}
	}
}

func unsafeLifecycleCandidate(method, pattern string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	if strings.Contains(pattern, "{user_id}") || strings.Contains(pattern, "{userId}") ||
		strings.Contains(pattern, "{account_id}") {
		return true
	}
	if strings.HasPrefix(pattern, "/api/v1/admin/users/{id}") ||
		strings.HasPrefix(pattern, "/api/v1/admin/tenants") ||
		strings.HasPrefix(pattern, "/api/v2/admin/platform/organizations/{id}") ||
		pattern == "/api/v1/auth/setup" || pattern == "/api/v1/auth/signup" ||
		strings.HasPrefix(pattern, "/api/v1/invitations/{token}") ||
		strings.HasPrefix(pattern, "/api/v1/profiles") {
		return true
	}
	return false
}

func normalizeLifecycleRoutePair(pair string) string {
	return strings.TrimSuffix(pair, "/")
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
		{http.MethodPatch, "/api/v1/admin/tenants/bf64e282-8c30-4bcc-8166-9047e52cb623/limits", true},
		{http.MethodDelete, "/api/v1/admin/tenants/bf64e282-8c30-4bcc-8166-9047e52cb623/members/42/auth-sessions/session-1", true},
		{http.MethodPost, "/api/v1/invitations/secret-token/accept", true},
		{http.MethodPatch, "/api/v2/admin/platform/organizations/bf64e282-8c30-4bcc-8166-9047e52cb623", true},
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
