package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/settingskeys"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func TestViewerResolverParityWithLegacyResolver(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	pdp := newViewerResolverTestPDP(t, ctx)

	tests := []struct {
		name             string
		user             *models.User
		profile          *userstore.Profile
		settings         map[string]string
		settingValues    []userstore.SettingValue
		input            access.ResolveInput
		tokens           access.ProfileTokenValidator
		wantEmptyAllowed bool
		wantMetadataLang string
	}{
		{
			name: "no profile unrestricted",
			user: &models.User{
				ID:                   1,
				AccessPolicyRevision: 5,
			},
			settings: map[string]string{"disabled_library_ids": "[7]"},
			input:    access.ResolveInput{UserID: 1, SessionID: "sess-1"},
		},
		{
			name: "profile unrestricted",
			user: &models.User{
				ID:                   1,
				MaxPlaybackQuality:   ptr("any"),
				AccessPolicyRevision: 5,
			},
			profile: &userstore.Profile{
				ID:                        "prof-1",
				MaxContentRating:          "PG-13",
				MaxPlaybackQuality:        "4k",
				PreferredMetadataLanguage: "fr",
			},
			input: access.ResolveInput{UserID: 1, SessionID: "sess-1", ProfileID: "prof-1"},
		},
		{
			name: "account and profile restrictions intersect",
			user: &models.User{
				ID:                   1,
				LibraryIDs:           []int{1, 2, 3},
				AccessPolicyRevision: 5,
			},
			profile: &userstore.Profile{
				ID:                         "prof-1",
				LibraryRestrictionsEnabled: true,
				AllowedLibraryIDs:          []int{2, 3, 4},
			},
			input: access.ResolveInput{UserID: 1, SessionID: "sess-1", ProfileID: "prof-1"},
		},
		{
			name: "restricted scope subtracts disabled libraries",
			user: &models.User{
				ID:                   1,
				LibraryIDs:           []int{1, 2, 3, 4},
				AccessPolicyRevision: 5,
			},
			profile:  &userstore.Profile{ID: "prof-1"},
			settings: map[string]string{"disabled_library_ids": "[2,4]"},
			input:    access.ResolveInput{UserID: 1, SessionID: "sess-1", ProfileID: "prof-1"},
		},
		{
			name: "unrestricted scope carries disabled libraries",
			user: &models.User{
				ID:                   1,
				AccessPolicyRevision: 5,
			},
			profile:  &userstore.Profile{ID: "prof-1"},
			settings: map[string]string{"disabled_library_ids": "[3,5]"},
			input:    access.ResolveInput{UserID: 1, SessionID: "sess-1", ProfileID: "prof-1"},
		},
		{
			name: "empty restricted library set stays non nil",
			user: &models.User{
				ID:                   1,
				LibraryIDs:           []int{1},
				AccessPolicyRevision: 5,
			},
			profile: &userstore.Profile{
				ID:                         "prof-1",
				LibraryRestrictionsEnabled: true,
				AllowedLibraryIDs:          []int{2},
			},
			input:            access.ResolveInput{UserID: 1, SessionID: "sess-1", ProfileID: "prof-1"},
			wantEmptyAllowed: true,
		},
		{
			name: "pin profile with skip verification",
			user: &models.User{
				ID:                   1,
				AccessPolicyRevision: 5,
			},
			profile: &userstore.Profile{
				ID:      "prof-1",
				PINHash: "pin-hash",
			},
			input: access.ResolveInput{
				UserID:              1,
				SessionID:           "sess-1",
				ProfileID:           "prof-1",
				SkipPINVerification: true,
			},
		},
		{
			name: "pin profile with valid token",
			user: &models.User{
				ID:                   1,
				AccessPolicyRevision: 5,
			},
			profile: &userstore.Profile{
				ID:      "prof-1",
				PINHash: "pin-hash",
			},
			input: access.ResolveInput{
				UserID:       1,
				SessionID:    "sess-1",
				ProfileID:    "prof-1",
				ProfileToken: "valid",
			},
			tokens: stubProfileTokenValidator{
				claims: &access.ProfileTokenClaims{
					UserID:         1,
					SessionID:      "sess-1",
					ProfileID:      "prof-1",
					PolicyRevision: 5,
				},
			},
		},
		{
			name: "quality and rating ceilings use policy normalization",
			user: &models.User{
				ID:                   1,
				MaxPlaybackQuality:   ptr("2160P"),
				AccessPolicyRevision: 5,
			},
			profile: &userstore.Profile{
				ID:                        "prof-1",
				MaxContentRating:          "PG-13",
				MaxPlaybackQuality:        "standard",
				PreferredMetadataLanguage: "de",
			},
			input: access.ResolveInput{UserID: 1, SessionID: "sess-1", ProfileID: "prof-1"},
		},
		{
			// The canonical catalog.metadata_language row feeds the policy input
			// and comes back out on the scope; the legacy profile column carries
			// a decoy value that must no longer be read.
			name: "metadata language resolves canonically",
			user: &models.User{
				ID:                   1,
				AccessPolicyRevision: 5,
			},
			profile: &userstore.Profile{
				ID:                        "prof-1",
				PreferredMetadataLanguage: "fr",
			},
			settingValues: []userstore.SettingValue{{
				SettingIdentity: userstore.SettingIdentity{
					Key:       settingskeys.CatalogMetadataLanguage,
					Scope:     settingscontract.ScopeProfile,
					ProfileID: "prof-1",
				},
				Value: json.RawMessage(`"de"`),
			}},
			input:            access.ResolveInput{UserID: 1, SessionID: "sess-1", ProfileID: "prof-1"},
			wantMetadataLang: "de",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := viewerResolverTestStore{
				profile:       tt.profile,
				settings:      tt.settings,
				settingValues: tt.settingValues,
			}
			users := viewerResolverUserRepo{user: tt.user}
			stores := viewerResolverStoreProvider{store: store}
			legacyResolver := access.NewResolver(users, stores, tt.tokens)
			viewerResolver := NewViewerResolver(users, stores, tt.tokens, pdp, defaultViewerResolverTenantLibraries())

			legacyScope, legacyErr := legacyResolver.Resolve(ctx, tt.input)
			policyScope, policyErr := viewerResolver.Resolve(ctx, tt.input)
			if legacyErr != nil || policyErr != nil {
				t.Fatalf("Resolve() errors: legacy=%v policy=%v", legacyErr, policyErr)
			}
			boundedLegacyScope := boundLegacyScopeToTenant(legacyScope, defaultViewerResolverTenantLibraries().ids)
			if !reflect.DeepEqual(policyScope, boundedLegacyScope) {
				t.Fatalf("scope mismatch\npolicy: %#v\nbounded legacy: %#v\nlegacy: %#v", policyScope, boundedLegacyScope, legacyScope)
			}
			assertCatalogAndPlaybackAuthorizationParity(t, policyScope, legacyScope, defaultViewerResolverTenantLibraries().ids)
			if tt.wantEmptyAllowed {
				if policyScope.AllowedLibraryIDs == nil || len(policyScope.AllowedLibraryIDs) != 0 {
					t.Fatalf("AllowedLibraryIDs = %#v, want non-nil empty slice", policyScope.AllowedLibraryIDs)
				}
			}
			// Always asserted: cases with only the legacy profile column expect
			// "" — the canonical resolution's contract default — proving the
			// column is no longer read.
			if policyScope.PreferredMetadataLanguage != tt.wantMetadataLang {
				t.Fatalf("PreferredMetadataLanguage = %q, want %q", policyScope.PreferredMetadataLanguage, tt.wantMetadataLang)
			}

			decisionInput := viewerResolverExpectedInput(tt.user, tt.profile, tt.input, policyScope.ProfileVerified, access.DisabledLibraryIDs(ctx, store, tt.input.ProfileID), access.PreferredMetadataLanguage(ctx, store, tt.input.ProfileID))
			decision, _, err := pdp.ResolveViewerScope(ctx, decisionInput)
			if err != nil {
				t.Fatalf("ResolveViewerScope() error: %v", err)
			}
			if decision.ProfileVerified != policyScope.ProfileVerified {
				t.Fatalf("decision ProfileVerified = %t, scope ProfileVerified = %t", decision.ProfileVerified, policyScope.ProfileVerified)
			}
			if want := access.ApplyGroupPolicy(tt.user, nil).MaxPlaybackQuality; decisionInput.AccountMaxQuality != want {
				t.Fatalf("AccountMaxQuality = %q, want resolved %q", decisionInput.AccountMaxQuality, want)
			}
			if decisionInput.IsAPIKey {
				t.Fatal("IsAPIKey = true, want false because ResolveInput cannot truthfully distinguish API keys")
			}
			if decisionInput.DeviceID != "" || decisionInput.ClientIP != "" {
				t.Fatalf("request identity fields = device %q client %q, want empty", decisionInput.DeviceID, decisionInput.ClientIP)
			}
			if _, err := time.Parse(time.RFC3339, decisionInput.RequestTime); err != nil {
				t.Fatalf("RequestTime = %q, want RFC3339: %v", decisionInput.RequestTime, err)
			}
		})
	}
}

func TestTenantFactsFromContextRequiresCompleteResolvedContext(t *testing.T) {
	_, err := TenantFactsFromContext(context.Background(), 1)
	if !errors.Is(err, ErrTenantFactsUnavailable) {
		t.Fatalf("error = %v, want ErrTenantFactsUnavailable", err)
	}

	tests := []struct {
		name   string
		mutate func(*tenancy.Context)
	}{
		{name: "missing organization id", mutate: func(tenant *tenancy.Context) { tenant.OrganizationID = uuid.Nil }},
		{name: "missing membership id", mutate: func(tenant *tenancy.Context) { tenant.MembershipID = uuid.Nil }},
		{name: "zero policy revision", mutate: func(tenant *tenancy.Context) { tenant.PolicyRevision = 0 }},
		{name: "zero security revision", mutate: func(tenant *tenancy.Context) { tenant.SecurityRevision = 0 }},
		{name: "suspended organization", mutate: func(tenant *tenancy.Context) { tenant.OrganizationStatus = tenancy.OrganizationSuspended }},
		{name: "suspended membership", mutate: func(tenant *tenancy.Context) { tenant.MembershipStatus = tenancy.MembershipSuspended }},
		{name: "non-legacy initializing organization", mutate: func(tenant *tenancy.Context) {
			tenant.Legacy = false
			tenant.OrganizationStatus = tenancy.OrganizationInitializing
		}},
		{name: "non-default legacy initializing organization", mutate: func(tenant *tenancy.Context) {
			tenant.OrganizationDefault = false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tenant := resolvedTenantForPolicyTest()
			test.mutate(&tenant)
			_, err := TenantFactsFromContext(tenancy.WithContext(context.Background(), tenant), tenant.AccountID)
			if !errors.Is(err, ErrTenantFactsUnavailable) {
				t.Fatalf("error = %v, want ErrTenantFactsUnavailable", err)
			}
		})
	}
}

func TestTenantFactsFromContextRequiresMatchingPositiveAccount(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	for _, expectedAccountID := range []int{0, 2} {
		_, err := TenantFactsFromContext(ctx, expectedAccountID)
		if !errors.Is(err, ErrTenantFactsUnavailable) {
			t.Fatalf("TenantFactsFromContext(expected account %d) error = %v, want ErrTenantFactsUnavailable", expectedAccountID, err)
		}
	}
}

func TestTenantFactsFromContextAcceptsActiveNonDefaultOrganization(t *testing.T) {
	tenant := resolvedTenantForPolicyTest()
	tenant.Legacy = false
	tenant.OrganizationDefault = false
	tenant.OrganizationStatus = tenancy.OrganizationActive

	facts, err := TenantFactsFromContext(tenancy.WithContext(context.Background(), tenant), tenant.AccountID)
	if err != nil {
		t.Fatalf("TenantFactsFromContext() error: %v", err)
	}
	if facts.Legacy || facts.OrganizationStatus != "active" {
		t.Fatalf("facts = %+v, want active non-legacy tenant", facts)
	}
}

func TestTenantFactsFromContextMarshalsExactFacts(t *testing.T) {
	facts, err := TenantFactsFromContext(resolvedTenantContextForPolicyTest(), 1)
	if err != nil {
		t.Fatalf("TenantFactsFromContext() error: %v", err)
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	want := `{"present":true,"legacy":true,"organization_id":"10000000-0000-0000-0000-000000000001","membership_id":"20000000-0000-0000-0000-000000000001","organization_status":"initializing","membership_status":"active","organization_policy_revision":7,"membership_security_revision":11}`
	if string(raw) != want {
		t.Fatalf("tenant JSON = %s, want %s", raw, want)
	}
}

func TestViewerResolverRejectsMissingTenantFacts(t *testing.T) {
	users := viewerResolverUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}}
	stores := viewerResolverStoreProvider{store: viewerResolverTestStore{}}
	resolver := NewViewerResolver(users, stores, nil, newViewerResolverTestPDP(t, context.Background()), defaultViewerResolverTenantLibraries())

	_, err := resolver.Resolve(context.Background(), access.ResolveInput{UserID: 1, SessionID: "sess-1"})
	if !errors.Is(err, ErrTenantFactsUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrTenantFactsUnavailable", err)
	}
}

func TestViewerResolverRejectsTenantForDifferentAccount(t *testing.T) {
	tenant := resolvedTenantForPolicyTest()
	tenant.AccountID = 2
	ctx := tenancy.WithContext(context.Background(), tenant)
	users := viewerResolverUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}}
	stores := viewerResolverStoreProvider{store: viewerResolverTestStore{}}
	resolver := NewViewerResolver(users, stores, nil, newViewerResolverTestPDP(t, context.Background()), defaultViewerResolverTenantLibraries())

	_, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, SessionID: "sess-1"})
	if !errors.Is(err, ErrTenantFactsUnavailable) {
		t.Fatalf("Resolve() error = %v, want ErrTenantFactsUnavailable", err)
	}
}

func TestViewerResolverTenantScopeLoadsVisibleLibraries(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	libraries := &viewerResolverTenantLibraries{ids: []int{20, 10, 20}}
	resolver := NewViewerResolver(
		viewerResolverUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}},
		viewerResolverStoreProvider{store: viewerResolverTestStore{}},
		nil,
		newViewerResolverTestPDP(t, ctx),
		libraries,
	)

	scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if !scope.LibrariesRestricted || !reflect.DeepEqual(scope.AllowedLibraryIDs, []int{10, 20}) {
		t.Fatalf("tenant-bounded scope = %#v, want restricted [10 20]", scope)
	}
	if len(libraries.tenants) != 1 || libraries.tenants[0] != resolvedTenantForPolicyTest() {
		t.Fatalf("availability tenants = %#v, want exact resolved tenant", libraries.tenants)
	}
}

func TestViewerResolverTenantScopeFailsClosedWithoutAvailability(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	users := viewerResolverUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}}
	stores := viewerResolverStoreProvider{store: viewerResolverTestStore{}}
	pdp := newViewerResolverTestPDP(t, ctx)

	t.Run("missing resolver", func(t *testing.T) {
		resolver := NewViewerResolver(users, stores, nil, pdp, nil)
		scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, SessionID: "sess-1"})
		if err == nil {
			t.Fatal("Resolve() error = nil, want tenant availability error")
		}
		assertZeroScope(t, scope)
	})

	t.Run("availability error", func(t *testing.T) {
		availabilityErr := errors.New("availability query failed")
		resolver := NewViewerResolver(users, stores, nil, pdp, &viewerResolverTenantLibraries{err: availabilityErr})
		scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, SessionID: "sess-1"})
		if !errors.Is(err, availabilityErr) {
			t.Fatalf("Resolve() error = %v, want wrapped availability error", err)
		}
		assertZeroScope(t, scope)
	})
}

func TestViewerResolverCustomPolicyUsingLegacyFieldsKeepsDecision(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	engine, err := NewEngineWithCustom(ctx, map[string]ActiveSource{
		DomainScope: {Source: `package silo_custom.scope

import rego.v1

override(_, request) := {"max_playback_quality": "720p"} if {
	request.user_id == 1
	request.session_id == "sess-1"
	request.tenant.organization_id == "10000000-0000-0000-0000-000000000001"
	request.tenant.membership_security_revision == 11
}`},
	})
	if err != nil {
		t.Fatalf("NewEngineWithCustom() error: %v", err)
	}
	resolver := NewViewerResolver(
		viewerResolverUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5, MaxPlaybackQuality: ptr("2160p")}},
		viewerResolverStoreProvider{store: viewerResolverTestStore{}},
		nil,
		NewPDP(engine),
		defaultViewerResolverTenantLibraries(),
	)

	scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if scope.MaxPlaybackQuality != "1080p" {
		t.Fatalf("MaxPlaybackQuality = %q, want normalized old-field custom decision 1080p", scope.MaxPlaybackQuality)
	}
	if !reflect.DeepEqual(scope.AllowedLibraryIDs, defaultViewerResolverTenantLibraries().ids) {
		t.Fatalf("AllowedLibraryIDs = %#v, want unchanged tenant bound %#v", scope.AllowedLibraryIDs, defaultViewerResolverTenantLibraries().ids)
	}
}

func TestPlaybackAdmissionAdapterRequiresAndPopulatesTenantFacts(t *testing.T) {
	checker := &capturingPolicyActionChecker{decision: ActionDecision{Allowed: true}}
	decider := NewPlaybackAdmissionDecider(checker)
	req := playback.AdmissionRequest{UserID: 1}

	if _, err := decider(context.Background(), req); !errors.Is(err, ErrTenantFactsUnavailable) {
		t.Fatalf("missing tenant error = %v, want ErrTenantFactsUnavailable", err)
	}
	if len(checker.inputs) != 0 {
		t.Fatalf("checker calls after missing tenant = %d, want 0", len(checker.inputs))
	}
	if _, err := decider(resolvedTenantContextForPolicyTest(), playback.AdmissionRequest{UserID: 2}); !errors.Is(err, ErrTenantFactsUnavailable) {
		t.Fatalf("mismatched account error = %v, want ErrTenantFactsUnavailable", err)
	}
	if len(checker.inputs) != 0 {
		t.Fatalf("checker calls after mismatched account = %d, want 0", len(checker.inputs))
	}

	if _, err := decider(resolvedTenantContextForPolicyTest(), req); err != nil {
		t.Fatalf("resolved tenant decision error: %v", err)
	}
	if len(checker.inputs) != 1 {
		t.Fatalf("checker calls = %d, want 1", len(checker.inputs))
	}
	if checker.inputs[0].Tenant != validLegacyTenantFactsForPolicyTest() {
		t.Fatalf("tenant facts = %+v, want %+v", checker.inputs[0].Tenant, validLegacyTenantFactsForPolicyTest())
	}
}

type capturingPolicyActionChecker struct {
	inputs   []ActionInput
	decision ActionDecision
}

func (c *capturingPolicyActionChecker) CheckAction(_ context.Context, input ActionInput) (ActionDecision, Meta, error) {
	c.inputs = append(c.inputs, input)
	return c.decision, Meta{}, nil
}

func resolvedTenantContextForPolicyTest() context.Context {
	return tenancy.WithContext(context.Background(), resolvedTenantForPolicyTest())
}

func resolvedTenantForPolicyTest() tenancy.Context {
	return tenancy.Context{
		OrganizationID:      uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		MembershipID:        uuid.MustParse("20000000-0000-0000-0000-000000000001"),
		AccountID:           1,
		OrganizationStatus:  tenancy.OrganizationInitializing,
		MembershipStatus:    tenancy.MembershipActive,
		PolicyRevision:      7,
		SecurityRevision:    11,
		Legacy:              true,
		OrganizationDefault: true,
	}
}

func TestViewerResolverPINErrorsMatchLegacy(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	user := &models.User{
		ID:                   1,
		AccessPolicyRevision: 5,
	}
	profile := &userstore.Profile{
		ID:      "prof-1",
		PINHash: "pin-hash",
	}
	pdp := newViewerResolverTestPDP(t, ctx)

	tests := []struct {
		name   string
		input  access.ResolveInput
		tokens access.ProfileTokenValidator
	}{
		{
			name: "no token validator",
			input: access.ResolveInput{
				UserID:    1,
				SessionID: "sess-1",
				ProfileID: "prof-1",
			},
		},
		{
			name: "bad token",
			input: access.ResolveInput{
				UserID:       1,
				SessionID:    "sess-1",
				ProfileID:    "prof-1",
				ProfileToken: "bad",
			},
			tokens: stubProfileTokenValidator{
				err: fmt.Errorf("%w: bad token", access.ErrProfileUnverified),
			},
		},
		{
			name: "revision mismatch",
			input: access.ResolveInput{
				UserID:       1,
				SessionID:    "sess-1",
				ProfileID:    "prof-1",
				ProfileToken: "valid",
			},
			tokens: stubProfileTokenValidator{
				claims: &access.ProfileTokenClaims{
					UserID:         1,
					SessionID:      "sess-1",
					ProfileID:      "prof-1",
					PolicyRevision: 4,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := viewerResolverUserRepo{user: user}
			stores := viewerResolverStoreProvider{store: viewerResolverTestStore{profile: profile}}
			legacyResolver := access.NewResolver(users, stores, tt.tokens)
			viewerResolver := NewViewerResolver(users, stores, tt.tokens, pdp, defaultViewerResolverTenantLibraries())

			_, legacyErr := legacyResolver.Resolve(ctx, tt.input)
			policyScope, policyErr := viewerResolver.Resolve(ctx, tt.input)
			if !errors.Is(legacyErr, access.ErrProfileUnverified) {
				t.Fatalf("legacy error = %v, want ErrProfileUnverified", legacyErr)
			}
			if !errors.Is(policyErr, access.ErrProfileUnverified) {
				t.Fatalf("policy error = %v, want ErrProfileUnverified", policyErr)
			}
			assertZeroScope(t, policyScope)
		})
	}
}

func TestViewerResolverProfileNotFoundMatchesLegacy(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	user := &models.User{ID: 1, AccessPolicyRevision: 5}
	users := viewerResolverUserRepo{user: user}
	stores := viewerResolverStoreProvider{store: viewerResolverTestStore{}}
	input := access.ResolveInput{UserID: 1, SessionID: "sess-1", ProfileID: "missing"}
	legacyResolver := access.NewResolver(users, stores, nil)
	viewerResolver := NewViewerResolver(users, stores, nil, newViewerResolverTestPDP(t, ctx), defaultViewerResolverTenantLibraries())

	_, legacyErr := legacyResolver.Resolve(ctx, input)
	policyScope, policyErr := viewerResolver.Resolve(ctx, input)
	if !errors.Is(legacyErr, access.ErrProfileNotFound) {
		t.Fatalf("legacy error = %v, want ErrProfileNotFound", legacyErr)
	}
	if !errors.Is(policyErr, access.ErrProfileNotFound) {
		t.Fatalf("policy error = %v, want ErrProfileNotFound", policyErr)
	}
	assertZeroScope(t, policyScope)
}

func TestViewerResolverEvalFailureFailsClosed(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	users := viewerResolverUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}}
	stores := viewerResolverStoreProvider{store: viewerResolverTestStore{}}
	resolver := NewViewerResolver(users, stores, nil, NewPDP(newEngine()), defaultViewerResolverTenantLibraries())

	scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, SessionID: "sess-1"})
	if err == nil {
		t.Fatal("Resolve() error = nil, want policy evaluation error")
	}
	if errors.Is(err, access.ErrProfileNotFound) || errors.Is(err, access.ErrProfileUnverified) {
		t.Fatalf("Resolve() error = %v, want wrapped internal policy error", err)
	}
	assertZeroScope(t, scope)
}

func TestViewerResolverPolicyRevokedProfileVerification(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	users := viewerResolverUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}}
	stores := viewerResolverStoreProvider{store: viewerResolverTestStore{}}
	engine, err := NewEngineWithCustom(ctx, map[string]ActiveSource{
		"scope": {Source: `package silo_custom.scope

import rego.v1

override(_, _) := {"profile_verified": false}
`},
	})
	if err != nil {
		t.Fatalf("NewEngineWithCustom() error: %v", err)
	}
	resolver := NewViewerResolver(users, stores, nil, NewPDP(engine), defaultViewerResolverTenantLibraries())

	scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, SessionID: "sess-1"})
	if !errors.Is(err, access.ErrProfileUnverified) {
		t.Fatalf("Resolve() error = %v, want ErrProfileUnverified when policy revokes verification", err)
	}
	assertZeroScope(t, scope)
}

func TestViewerResolverAppliesGroupPolicy(t *testing.T) {
	organizationID := uuid.New()
	tenant := resolvedTenantForPolicyTest()
	tenant.OrganizationID = organizationID
	ctx := tenancy.WithContext(context.Background(), tenant)
	groupID := int64(2)
	user := &models.User{
		ID:                   1,
		AccessGroupID:        &groupID,
		AccessPolicyRevision: 5,
	}
	group := &access.GroupPolicy{
		LibraryIDs:               []int{2, 4},
		MaxPlaybackQuality:       access.PlaybackQualityStandard,
		PlaybackAllowed:          true,
		TranscodeAllowed:         true,
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: true,
		TranscodeAllowed:         true,
		AudioTranscodeAllowed:    true,
		RequestsAllowed:          true,
	}
	users := viewerResolverUserRepo{user: user}
	stores := viewerResolverStoreProvider{store: viewerResolverTestStore{profile: &userstore.Profile{
		ID:             "prof-1",
		OrganizationID: organizationID.String(),
	}}}
	resolver := NewViewerResolver(
		users,
		stores,
		nil,
		newViewerResolverTestPDP(t, ctx),
		defaultViewerResolverTenantLibraries(),
		&viewerResolverGroupProvider{group: group},
	)

	scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, SessionID: "sess-1", ProfileID: "prof-1"})
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if !scope.LibrariesRestricted || !reflect.DeepEqual(scope.AllowedLibraryIDs, []int{2, 4}) {
		t.Fatalf("scope libraries = restricted %t ids %#v, want [2 4]", scope.LibrariesRestricted, scope.AllowedLibraryIDs)
	}
	if scope.MaxPlaybackQuality != access.PlaybackQualityStandard {
		t.Fatalf("MaxPlaybackQuality = %q, want %q", scope.MaxPlaybackQuality, access.PlaybackQualityStandard)
	}
}

func TestViewerResolverLoadsProfileBeforeResolvingTenantGroup(t *testing.T) {
	organizationID := uuid.New()
	events := []string{}
	store := orderedViewerResolverStore{
		viewerResolverTestStore: viewerResolverTestStore{profile: &userstore.Profile{
			ID:             "prof-1",
			OrganizationID: organizationID.String(),
		}},
		events: &events,
	}
	groups := &viewerResolverGroupProvider{
		group:  &access.GroupPolicy{PlaybackAllowed: true, TranscodeAllowed: true, DownloadAllowed: true, DownloadTranscodeAllowed: true, RequestsAllowed: true},
		events: &events,
	}
	libraries := &viewerResolverTenantLibraries{ids: defaultViewerResolverTenantLibraries().ids, events: &events}
	tenant := resolvedTenantForPolicyTest()
	tenant.OrganizationID = organizationID
	ctx := tenancy.WithContext(context.Background(), tenant)
	resolver := NewViewerResolver(
		viewerResolverUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}},
		viewerResolverStoreProvider{store: store},
		nil,
		newViewerResolverTestPDP(t, ctx),
		libraries,
		groups,
	)

	if _, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, ProfileID: "prof-1"}); err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if !reflect.DeepEqual(events, []string{"profile", "group", "libraries"}) {
		t.Fatalf("resolution order = %#v, want validated profile/group before tenant libraries", events)
	}
	wantSubject := access.GroupSubject{OrganizationID: organizationID, AccountID: 1, ProfileID: "prof-1", Legacy: true}
	if groups.subject != wantSubject {
		t.Fatalf("ResolvePolicy subject = %#v, want %#v", groups.subject, wantSubject)
	}
}

type stubProfileTokenValidator struct {
	claims *access.ProfileTokenClaims
	err    error
}

func (v stubProfileTokenValidator) Validate(string) (*access.ProfileTokenClaims, error) {
	if v.err != nil {
		return nil, v.err
	}
	return v.claims, nil
}

type viewerResolverUserRepo struct {
	user *models.User
	err  error
}

type viewerResolverGroupProvider struct {
	group   *access.GroupPolicy
	err     error
	subject access.GroupSubject
	events  *[]string
}

type viewerResolverTenantLibraries struct {
	ids     []int
	err     error
	tenants []tenancy.Context
	events  *[]string
}

func defaultViewerResolverTenantLibraries() *viewerResolverTenantLibraries {
	return &viewerResolverTenantLibraries{ids: []int{1, 2, 3, 4, 5, 7}}
}

func (r *viewerResolverTenantLibraries) AvailableMediaFolderIDs(_ context.Context, tenant tenancy.Context) ([]int, error) {
	r.tenants = append(r.tenants, tenant)
	if r.events != nil {
		*r.events = append(*r.events, "libraries")
	}
	return slices.Clone(r.ids), r.err
}

func (p *viewerResolverGroupProvider) ResolvePolicy(_ context.Context, subject access.GroupSubject) (*access.GroupPolicy, error) {
	p.subject = subject
	if p.events != nil {
		*p.events = append(*p.events, "group")
	}
	return p.group, p.err
}

func (r viewerResolverUserRepo) GetByID(_ context.Context, id int) (*models.User, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.user == nil || r.user.ID != id {
		return nil, errors.New("user not found")
	}
	return r.user, nil
}

type viewerResolverStoreProvider struct {
	store userstore.UserStore
	err   error
}

func (p viewerResolverStoreProvider) ForUser(context.Context, int) (userstore.UserStore, error) {
	return p.store, p.err
}

func (p viewerResolverStoreProvider) Close() error {
	return nil
}

type viewerResolverTestStore struct {
	userstore.UserStore
	profile  *userstore.Profile
	err      error
	settings map[string]string
	// settingValues are the canonical setting rows the resolver may read
	// through ListSettingValuesForResolution. Scope matching is the
	// resolver's job, so the store returns them unfiltered.
	settingValues []userstore.SettingValue
}

type orderedViewerResolverStore struct {
	viewerResolverTestStore
	events *[]string
}

func (s orderedViewerResolverStore) GetProfile(ctx context.Context, id string) (*userstore.Profile, error) {
	*s.events = append(*s.events, "profile")
	return s.viewerResolverTestStore.GetProfile(ctx, id)
}

type countingViewerResolverStore struct {
	viewerResolverTestStore
	resolutionReads int
}

func (s *countingViewerResolverStore) ListSettingValuesForResolution(
	ctx context.Context, query userstore.SettingResolutionQuery,
) ([]userstore.SettingValue, error) {
	s.resolutionReads++
	return s.viewerResolverTestStore.ListSettingValuesForResolution(ctx, query)
}

func TestViewerResolverBatchesViewerPreferenceRead(t *testing.T) {
	ctx := resolvedTenantContextForPolicyTest()
	store := &countingViewerResolverStore{viewerResolverTestStore: viewerResolverTestStore{
		profile: &userstore.Profile{ID: "prof-1"},
		settingValues: []userstore.SettingValue{
			{
				SettingIdentity: userstore.SettingIdentity{
					Key: settingskeys.UiDisabledLibraryIds, Scope: settingscontract.ScopeProfile,
					ProfileID: "prof-1",
				},
				Value: json.RawMessage(`[3,5]`),
			},
			{
				SettingIdentity: userstore.SettingIdentity{
					Key: settingskeys.CatalogMetadataLanguage, Scope: settingscontract.ScopeProfile,
					ProfileID: "prof-1",
				},
				Value: json.RawMessage(`"de"`),
			},
			{
				SettingIdentity: userstore.SettingIdentity{
					Key: settingskeys.CatalogMetadataLanguageOverrides, Scope: settingscontract.ScopeProfile,
					ProfileID: "prof-1",
				},
				Value: json.RawMessage(`{"no":"x-silo-original"}`),
			},
		},
	}}
	resolver := NewViewerResolver(
		viewerResolverUserRepo{user: &models.User{ID: 1, AccessPolicyRevision: 5}},
		viewerResolverStoreProvider{store: store}, nil, newViewerResolverTestPDP(t, ctx),
		defaultViewerResolverTenantLibraries(),
	)

	scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: 1, ProfileID: "prof-1"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if store.resolutionReads != 1 {
		t.Fatalf("canonical preference reads = %d, want 1", store.resolutionReads)
	}
	if !reflect.DeepEqual(scope.AllowedLibraryIDs, []int{1, 2, 4, 7}) || scope.DisabledLibraryIDs != nil || scope.PreferredMetadataLanguage != "de" {
		t.Errorf("resolved scope = %#v", scope)
	}
	if got := scope.MetadataLanguageOverrides["no"]; got != access.OriginalMetadataLanguage {
		t.Errorf("metadata language override = %q, want %q", got, access.OriginalMetadataLanguage)
	}
}

func (s viewerResolverTestStore) GetProfile(_ context.Context, id string) (*userstore.Profile, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.profile == nil || s.profile.ID != id {
		return nil, nil
	}
	return s.profile, nil
}

func (s viewerResolverTestStore) GetSetting(_ context.Context, key string) (string, error) {
	return s.settings[key], nil
}

func (s viewerResolverTestStore) ListSettingValuesForResolution(context.Context, userstore.SettingResolutionQuery) ([]userstore.SettingValue, error) {
	return s.settingValues, nil
}

func viewerResolverExpectedInput(
	user *models.User,
	profile *userstore.Profile,
	input access.ResolveInput,
	profileVerified bool,
	disabled []int,
	metadataLang string,
) ScopeInput {
	out := ScopeInput{
		SchemaVersion:        1,
		Tenant:               validLegacyTenantFactsForPolicyTest(),
		UserID:               user.ID,
		SessionID:            input.SessionID,
		ProfileID:            input.ProfileID,
		AccountLibraryIDs:    cloneViewerResolverInts(user.LibraryIDs),
		AccountRestricted:    user.LibraryIDs != nil,
		AccountMaxQuality:    access.ApplyGroupPolicy(user, nil).MaxPlaybackQuality,
		AccessPolicyRevision: user.AccessPolicyRevision,
		DisabledLibraryIDs:   cloneViewerResolverInts(disabled),
		ProfileVerified:      profileVerified,
		TenantLibraryIDs:     slices.Clone(defaultViewerResolverTenantLibraries().ids),
		RequestTime:          time.Now().UTC().Format(time.RFC3339),
		IsAPIKey:             false,
	}
	if profile != nil {
		out.ProfilePresent = true
		out.ProfileMaxRating = profile.MaxContentRating
		out.ProfileMaxQuality = profile.MaxPlaybackQuality
		out.ProfileLibraryLimited = profile.LibraryRestrictionsEnabled
		out.ProfileLibraryIDs = cloneViewerResolverInts(profile.AllowedLibraryIDs)
		out.ProfileHasPIN = profile.PINHash != ""
		// Canonically resolved, mirroring ViewerResolver — the legacy profile
		// column is no longer a policy input.
		out.ProfileMetadataLang = metadataLang
	}
	return out
}

func cloneViewerResolverInts(values []int) []int {
	if values == nil {
		return nil
	}
	out := make([]int, len(values))
	copy(out, values)
	return out
}

func newViewerResolverTestPDP(t *testing.T, ctx context.Context) *PDP {
	t.Helper()
	engine, err := NewEngine(ctx)
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}
	return NewPDP(engine)
}

func assertZeroScope(t *testing.T, scope access.Scope) {
	t.Helper()
	if scope.UserID != 0 ||
		scope.ProfileID != "" ||
		scope.AllowedLibraryIDs != nil ||
		scope.DisabledLibraryIDs != nil ||
		scope.LibrariesRestricted ||
		scope.MaxContentRating != "" ||
		scope.MaxPlaybackQuality != "" ||
		scope.PreferredMetadataLanguage != "" ||
		scope.PolicyRevision != 0 ||
		scope.ProfileVerified {
		t.Fatalf("scope = %#v, want zero Scope", scope)
	}
}

func ptr[T any](value T) *T { return &value }
