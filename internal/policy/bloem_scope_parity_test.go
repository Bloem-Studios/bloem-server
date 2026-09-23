package policy

// Bloem tenant-bound scope coverage moved out of Silo's scope_parity_test.go.

import (
	"reflect"
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

func TestTenantScopeBoundsUnrestrictedViewer(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	engine, err := NewEngine(ctx)
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	input := scopeInputFromParity(&models.User{ID: 42, AccessPolicyRevision: 9}, nil, nil, true)
	input.TenantLibraryIDs = []int{20, 10, 20}
	decision, _, err := NewPDP(engine).ResolveViewerScope(ctx, input)
	if err != nil {
		t.Fatalf("ResolveViewerScope() error: %v", err)
	}
	if decision.Unrestricted || !reflect.DeepEqual(decision.AllowedLibraryIDs, []int{10, 20}) {
		t.Fatalf("tenant-bounded decision = %#v, want restricted [10 20]", decision)
	}
}

func TestTenantScopeMissingUpperBoundFailsClosed(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	engine, err := NewEngine(ctx)
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	input := scopeInputFromParity(&models.User{ID: 42, AccessPolicyRevision: 9}, nil, nil, true)
	input.TenantLibraryIDs = nil
	decision, _, err := NewPDP(engine).ResolveViewerScope(ctx, input)
	if err != nil {
		t.Fatalf("ResolveViewerScope() error: %v", err)
	}
	if decision.Unrestricted || decision.AllowedLibraryIDs == nil || len(decision.AllowedLibraryIDs) != 0 {
		t.Fatalf("decision without tenant bound = %#v, want restricted non-nil empty allow-list", decision)
	}
}

func TestTenantScopeCannotBeWidenedByCustomPolicy(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	engine, err := NewEngineWithCustom(ctx, map[string]ActiveSource{
		DomainScope: {Source: `package silo_custom.scope

import rego.v1

override(_, _) := {"unrestricted": false, "allowed_library_ids": [10, 99]}
`},
	})
	if err != nil {
		t.Fatalf("NewEngineWithCustom() error: %v", err)
	}

	input := scopeInputFromParity(&models.User{ID: 42, AccessPolicyRevision: 9}, nil, nil, true)
	input.TenantLibraryIDs = []int{10}
	decision, _, err := NewPDP(engine).ResolveViewerScope(ctx, input)
	if err != nil {
		t.Fatalf("ResolveViewerScope() error: %v", err)
	}
	if decision.Unrestricted || !reflect.DeepEqual(decision.AllowedLibraryIDs, []int{10}) {
		t.Fatalf("custom tenant-bounded decision = %#v, want restricted [10]", decision)
	}
}

func boundLegacyScopeToTenant(scope access.Scope, tenantLibraryIDs []int) access.Scope {
	allowed := cloneParityInts(tenantLibraryIDs)
	if scope.AllowedLibraryIDs != nil {
		allowed = parityIntersect(allowed, scope.AllowedLibraryIDs)
	}
	allowed = paritySubtract(allowed, scope.DisabledLibraryIDs)
	slices.Sort(allowed)
	allowed = slices.Compact(allowed)
	scope.AllowedLibraryIDs = allowed
	scope.DisabledLibraryIDs = nil
	scope.LibrariesRestricted = true
	return scope
}

func assertCatalogAndPlaybackAuthorizationParity(t *testing.T, policyScope, legacyScope access.Scope, tenantLibraryIDs []int) {
	t.Helper()
	policyCatalogIDs := visibleLibraryIDs(policyScope, tenantLibraryIDs)
	legacyCatalogIDs := visibleLibraryIDs(legacyScope, tenantLibraryIDs)
	if !slices.Equal(policyCatalogIDs, legacyCatalogIDs) {
		t.Fatalf("catalog/search-visible library IDs mismatch: policy=%#v legacy=%#v", policyCatalogIDs, legacyCatalogIDs)
	}

	policyFilter := catalog.AccessFilter{
		AllowedLibraryIDs:  policyScope.AllowedLibraryIDs,
		DisabledLibraryIDs: policyScope.DisabledLibraryIDs,
		MaxPlaybackQuality: policyScope.MaxPlaybackQuality,
	}
	legacyFilter := catalog.AccessFilter{
		AllowedLibraryIDs:  legacyScope.AllowedLibraryIDs,
		DisabledLibraryIDs: legacyScope.DisabledLibraryIDs,
		MaxPlaybackQuality: legacyScope.MaxPlaybackQuality,
	}
	for _, id := range append(cloneParityInts(tenantLibraryIDs), 99) {
		file := &models.MediaFile{MediaFolderID: id, Resolution: "720p"}
		policyAllowed := catalog.FileAllowedByAccess(file, policyFilter)
		legacyAllowed := slices.Contains(tenantLibraryIDs, id) && catalog.FileAllowedByAccess(file, legacyFilter)
		if policyAllowed != legacyAllowed {
			t.Fatalf("playback authorization for library %d mismatch: policy=%t legacy-with-tenant-bound=%t", id, policyAllowed, legacyAllowed)
		}
	}
}

func visibleLibraryIDs(scope access.Scope, tenantLibraryIDs []int) []int {
	visible := cloneParityInts(tenantLibraryIDs)
	if scope.AllowedLibraryIDs != nil {
		visible = parityIntersect(visible, scope.AllowedLibraryIDs)
	}
	visible = paritySubtract(visible, scope.DisabledLibraryIDs)
	slices.Sort(visible)
	return slices.Compact(visible)
}

func parityIntersect(left, right []int) []int {
	out := make([]int, 0)
	for _, id := range left {
		if slices.Contains(right, id) {
			out = append(out, id)
		}
	}
	return out
}

func paritySubtract(values, excluded []int) []int {
	out := make([]int, 0)
	for _, id := range values {
		if !slices.Contains(excluded, id) {
			out = append(out, id)
		}
	}
	return out
}

func validLegacyTenantFactsForPolicyTest() TenantFacts {
	return TenantFacts{
		Present:                    true,
		Legacy:                     true,
		OrganizationID:             "10000000-0000-0000-0000-000000000001",
		MembershipID:               "20000000-0000-0000-0000-000000000001",
		OrganizationStatus:         "initializing",
		MembershipStatus:           "active",
		OrganizationPolicyRevision: 7,
		MembershipSecurityRevision: 11,
	}
}
