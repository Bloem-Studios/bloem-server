package policy

// Bloem tenancy coverage moved out of Silo's viewer_resolver_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

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

type orderedViewerResolverStore struct {
	viewerResolverTestStore
	events *[]string
}

func (s orderedViewerResolverStore) GetProfile(ctx context.Context, id string) (*userstore.Profile, error) {
	*s.events = append(*s.events, "profile")
	return s.viewerResolverTestStore.GetProfile(ctx, id)
}
