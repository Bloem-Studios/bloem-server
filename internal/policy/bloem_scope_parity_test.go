package policy

// Bloem tenant-bound scope coverage moved out of Silo's scope_parity_test.go.

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// bloemScopeInputFromParity is Silo's scopeInputFromParity with a resolved
// tenant whose libraries are 1-4.
func bloemScopeInputFromParity(user *models.User, profile *userstore.Profile, disabled []int, verified bool) ScopeInput {
	input := scopeInputFromParity(user, profile, disabled, verified)
	input.Tenant = validLegacyTenantFactsForPolicyTest()
	input.TenantLibraryIDs = []int{1, 2, 3, 4}
	return input
}

// TestResolveViewerScopeParityUnderTenancy runs the library cases of Silo's
// TestResolveViewerScopeParity with a resolved tenant: policy's scope must be
// Silo's Go scope bounded by the tenant's libraries, and catalog visibility
// and playback authorization must agree with it.
func TestResolveViewerScopeParityUnderTenancy(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	engine, err := NewEngine(ctx, WithEvalTimeout(time.Minute))
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}
	pdp := NewPDP(engine)

	accountCases := map[string][]int{"account_unrestricted": nil, "account_empty": {}, "account_1_2_3": {1, 2, 3}}
	profileCases := map[string]*userstore.Profile{
		"profile_absent":       nil,
		"profile_unrestricted": parityProfile(false, nil),
		"profile_empty":        parityProfile(true, []int{}),
		"profile_2_3_4":        parityProfile(true, []int{2, 3, 4}),
	}
	disabledCases := map[string][]int{"disabled_none": nil, "disabled_2": {2}}

	for accountName, accountLibraries := range accountCases {
		for profileName, profileCase := range profileCases {
			for disabledName, disabled := range disabledCases {
				t.Run(fmt.Sprintf("%s/%s/%s", accountName, profileName, disabledName), func(t *testing.T) {
					user := &models.User{ID: 42, LibraryIDs: cloneParityInts(accountLibraries), MaxPlaybackQuality: ptr("any"), AccessPolicyRevision: 9}
					profile := cloneParityProfile(profileCase)
					profileID := ""
					if profile != nil {
						profile.MaxPlaybackQuality = "standard"
						profileID = profile.ID
					}
					resolver := access.NewResolver(
						parityUserRepo{user: user},
						parityStoreProvider{store: parityStore{profile: profile, settingValues: parityPreferenceValues(profile, disabled)}},
						nil,
					)

					input := bloemScopeInputFromParity(user, profile, disabled, true)
					decision, _, err := pdp.ResolveViewerScope(ctx, input)
					if err != nil {
						t.Fatalf("ResolveViewerScope() error: %v", err)
					}
					goScope, err := resolver.Resolve(ctx, access.ResolveInput{
						UserID: user.ID, SessionID: input.SessionID, ProfileID: profileID, SkipPINVerification: profile != nil,
					})
					if err != nil && !errors.Is(err, access.ErrProfileUnverified) {
						t.Fatalf("Resolve() error: %v", err)
					}

					policyScope := decisionToAccessScope(input, decision)
					bounded := boundLegacyScopeToTenant(goScope, input.TenantLibraryIDs)
					if !reflect.DeepEqual(policyScope, bounded) {
						t.Fatalf("scope mismatch\npolicy: %#v\nbounded legacy: %#v\nlegacy: %#v\ndecision: %#v", policyScope, bounded, goScope, decision)
					}
					assertCatalogAndPlaybackAuthorizationParity(t, policyScope, goScope, input.TenantLibraryIDs)
				})
			}
		}
	}
}

func TestTenantScopeBoundsUnrestrictedViewer(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	engine, err := NewEngine(ctx)
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}

	input := bloemScopeInputFromParity(&models.User{ID: 42, AccessPolicyRevision: 9}, nil, nil, true)
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

	input := bloemScopeInputFromParity(&models.User{ID: 42, AccessPolicyRevision: 9}, nil, nil, true)
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

	input := bloemScopeInputFromParity(&models.User{ID: 42, AccessPolicyRevision: 9}, nil, nil, true)
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
	candidates := cloneParityInts(tenantLibraryIDs)
	if scope.AllowedLibraryIDs != nil {
		allowed = parityIntersect(allowed, scope.AllowedLibraryIDs)
		// A restricted legacy scope already took the libraries the profile
		// hid out of its allowed list and reports them as hidden.
		candidates = parityIntersect(candidates, append(cloneParityInts(scope.AllowedLibraryIDs), scope.HiddenLibraryIDs...))
	}
	// The tenant-bounded libraries the profile hid: an unrestricted legacy
	// scope carries them as disabled, a restricted one as hidden.
	hidden := parityIntersect(candidates, append(cloneParityInts(scope.HiddenLibraryIDs), scope.DisabledLibraryIDs...))
	slices.Sort(hidden)
	hidden = slices.Compact(hidden)
	scope.HiddenLibraryIDs = nil
	if len(hidden) > 0 {
		scope.HiddenLibraryIDs = hidden
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
