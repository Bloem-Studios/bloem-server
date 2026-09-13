package requests

// Bloem-owned. Covers the organization bound added in bloem_tenant_scope.go.
// Reuses Silo's fakeStore, newTestService and testViewer from service_test.go
// rather than editing that file: same package, so unexported helpers are
// reachable from a file Bloem owns outright.

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/metadata/tmdb"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type fakeTenantScope struct {
	orgs  map[int]uuid.UUID
	err   error
	calls int
}

func (f *fakeTenantScope) AccountOrganization(_ context.Context, accountID int) (uuid.UUID, error) {
	f.calls++
	if f.err != nil {
		return uuid.Nil, f.err
	}
	org, ok := f.orgs[accountID]
	if !ok {
		return uuid.Nil, errors.New("no membership")
	}
	return org, nil
}

// listAdminStore gives ListAdmin rows to filter; Silo's fakeStore returns nil.
type listAdminStore struct {
	*fakeStore
	rows []*Request
}

func (s *listAdminStore) ListAdmin(context.Context, ListFilter) ([]*Request, error) {
	return s.rows, nil
}

var (
	orgAlpha = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	orgBeta  = uuid.MustParse("22222222-2222-2222-2222-222222222222")
)

func adminViewer(userID int) Viewer {
	v := testViewer(userID)
	v.IsAdmin = true
	return v
}

// seedRequest stores one request owned by requesterID and returns its id.
func seedRequest(store *fakeStore, id string, requesterID int) string {
	store.requests[id] = &Request{
		ID:                id,
		RequestedByUserID: requesterID,
		MediaType:         MediaTypeMovie,
		Outcome:           OutcomeActive,
		Status:            StatusPending,
	}
	return id
}

// Silo authorizes with `!viewer.IsAdmin && req.RequestedByUserID !=
// viewer.UserID`, so an administrator reaches every tenant. This is the hole.
func TestBloemAdminCannotReadAnotherOrganizationsRequest(t *testing.T) {
	store := newFakeStore()
	id := seedRequest(store, "req-beta", 7)
	svc := newTestService(store)
	svc.SetTenantScopeResolver(&fakeTenantScope{orgs: map[int]uuid.UUID{
		1: orgAlpha, // the admin
		7: orgBeta,  // the requester, a different tenant
	}})

	if _, err := svc.GetRequest(context.Background(), adminViewer(1), id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetRequest across organizations = %v, want ErrForbidden", err)
	}
}

func TestBloemAdminReadsOwnOrganizationsRequest(t *testing.T) {
	store := newFakeStore()
	id := seedRequest(store, "req-alpha", 7)
	svc := newTestService(store)
	svc.SetTenantScopeResolver(&fakeTenantScope{orgs: map[int]uuid.UUID{1: orgAlpha, 7: orgAlpha}})

	if _, err := svc.GetRequest(context.Background(), adminViewer(1), id); err != nil {
		t.Fatalf("GetRequest within one organization: %v", err)
	}
}

// Mutating paths matter more than reads: an unbounded admin could approve or
// decline another tenant's request, which reaches that tenant's downloaders.
func TestBloemAdminCannotMutateAnotherOrganizationsRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Service, Viewer, string) error
	}{
		{"Approve", func(s *Service, v Viewer, id string) error {
			_, err := s.Approve(context.Background(), v, id)
			return err
		}},
		{"Decline", func(s *Service, v Viewer, id string) error {
			_, err := s.Decline(context.Background(), v, id, "no")
			return err
		}},
		{"Cancel", func(s *Service, v Viewer, id string) error {
			_, err := s.Cancel(context.Background(), v, id, "no")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			id := seedRequest(store, "req-beta", 7)
			svc := newTestService(store)
			svc.SetTenantScopeResolver(&fakeTenantScope{orgs: map[int]uuid.UUID{1: orgAlpha, 7: orgBeta}})

			if err := tc.call(svc, adminViewer(1), id); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s across organizations = %v, want ErrForbidden", tc.name, err)
			}
		})
	}
}

// Unset resolver must leave Silo's behavior exactly as it was, which is what
// every single-tenant deployment runs.
func TestBloemNilResolverPreservesSiloAdminAuthority(t *testing.T) {
	store := newFakeStore()
	id := seedRequest(store, "req-beta", 7)
	svc := newTestService(store) // no SetTenantScopeResolver

	if _, err := svc.GetRequest(context.Background(), adminViewer(1), id); err != nil {
		t.Fatalf("admin read with no resolver: %v", err)
	}
}

// A wired resolver that cannot answer must deny rather than fall through to
// Silo's unbounded check.
func TestBloemUnresolvableOrganizationFailsClosed(t *testing.T) {
	store := newFakeStore()
	id := seedRequest(store, "req-beta", 7)
	svc := newTestService(store)
	svc.SetTenantScopeResolver(&fakeTenantScope{err: errors.New("tenant store down")})

	if _, err := svc.GetRequest(context.Background(), adminViewer(1), id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetRequest with a failing resolver = %v, want ErrForbidden", err)
	}
}

// The requester's own path must not depend on the tenant store at all: it is
// the hot path, and a tenancy outage must not deny people their own requests.
func TestBloemRequesterOwnRequestSkipsTenantLookup(t *testing.T) {
	store := newFakeStore()
	id := seedRequest(store, "req-mine", 5)
	svc := newTestService(store)
	scope := &fakeTenantScope{err: errors.New("tenant store down")}
	svc.SetTenantScopeResolver(scope)

	if _, err := svc.GetRequest(context.Background(), testViewer(5), id); err != nil {
		t.Fatalf("requester reading their own request: %v", err)
	}
	if scope.calls != 0 {
		t.Fatalf("tenant lookups on the self-service path = %d, want 0", scope.calls)
	}
}

func TestBloemListAdminDropsOtherOrganizations(t *testing.T) {
	base := newFakeStore()
	store := &listAdminStore{fakeStore: base, rows: []*Request{
		{ID: "a", RequestedByUserID: 7, MediaType: MediaTypeMovie},
		{ID: "b", RequestedByUserID: 9, MediaType: MediaTypeMovie},
		{ID: "c", RequestedByUserID: 7, MediaType: MediaTypeMovie},
	}}
	svc := NewService(store, &fakeTMDBClient{}, &fakePresence{})
	svc.SetTenantScopeResolver(&fakeTenantScope{orgs: map[int]uuid.UUID{
		1: orgAlpha, 7: orgAlpha, 9: orgBeta,
	}})

	got, err := svc.ListAdmin(context.Background(), adminViewer(1), ListFilter{})
	if err != nil {
		t.Fatalf("ListAdmin: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListAdmin returned %d requests, want 2 (the beta row dropped)", len(got))
	}
	for _, req := range got {
		if req.RequestedByUserID == 9 {
			t.Fatalf("ListAdmin leaked request %q from another organization", req.ID)
		}
	}
}

// Per-account admin surfaces take a target user rather than a request, so they
// need the same bound.
func TestBloemUserLimitBoundToOrganization(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(store)
	svc.SetTenantScopeResolver(&fakeTenantScope{orgs: map[int]uuid.UUID{1: orgAlpha, 9: orgBeta}})

	if _, err := svc.GetUserLimit(context.Background(), adminViewer(1), 9); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetUserLimit across organizations = %v, want ErrForbidden", err)
	}
	if _, err := svc.UpsertUserLimit(context.Background(), adminViewer(1), UserLimit{
		UserID: 9, LimitMode: LimitModeInherit, ApprovalMode: ApprovalModeInherit,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("UpsertUserLimit across organizations = %v, want ErrForbidden", err)
	}
}

// boundedStore advertises the SQL-bounding capability and records what it was
// asked for, so the tests can prove the service routed there rather than to the
// unbounded query.
type boundedStore struct {
	*fakeStore
	listAdminOrg  uuid.UUID
	activeOrg     uuid.UUID
	deleteOrg     uuid.UUID
	listAdminRows []*Request
	activeCalls   int
	unboundedHits int
}

func (s *boundedStore) ListAdminInOrganization(_ context.Context, organizationID uuid.UUID, _ ListFilter) ([]*Request, error) {
	s.listAdminOrg = organizationID
	return s.listAdminRows, nil
}

func (s *boundedStore) ListActiveByTMDBInOrganization(_ context.Context, organizationID uuid.UUID, _ MediaType, _ []int) (map[int]*Request, error) {
	s.activeOrg = organizationID
	s.activeCalls++
	return map[int]*Request{}, nil
}

func (s *boundedStore) DeleteFailedByTMDBInOrganization(_ context.Context, organizationID uuid.UUID, _ MediaType, _ int) (int, error) {
	s.deleteOrg = organizationID
	return 0, nil
}

// ListActiveByTMDB is the unbounded query; reaching it with a resolver wired
// and a bounding store available would be the bug.
func (s *boundedStore) ListActiveByTMDB(context.Context, MediaType, []int) (map[int]*Request, error) {
	s.unboundedHits++
	return map[int]*Request{}, nil
}

func newBoundedService(store *boundedStore, orgs map[int]uuid.UUID) *Service {
	tmdbClient := &fakeTMDBClient{
		detail: &tmdb.MediaDetail{ID: 603, MediaType: "movie", Title: "The Matrix"},
	}
	svc := NewService(store, tmdbClient, &fakePresence{})
	svc.SetUserRepository(requestUserRepo{})
	svc.SetTenantScopeResolver(&fakeTenantScope{orgs: orgs})
	return svc
}

// Step 1 filtered ListAdmin after the query, so LIMIT counted rows the viewer
// could not see and pages came back short. With a bounding store the
// organization goes into the statement instead.
func TestBloemListAdminUsesTheSQLBoundWhenAvailable(t *testing.T) {
	store := &boundedStore{fakeStore: newFakeStore(), listAdminRows: []*Request{
		{ID: "a", RequestedByUserID: 7, MediaType: MediaTypeMovie},
	}}
	svc := newBoundedService(store, map[int]uuid.UUID{1: orgAlpha, 7: orgAlpha})

	got, err := svc.ListAdmin(context.Background(), adminViewer(1), ListFilter{})
	if err != nil {
		t.Fatalf("ListAdmin: %v", err)
	}
	if store.listAdminOrg != orgAlpha {
		t.Fatalf("bounded ListAdmin organization = %v, want %v", store.listAdminOrg, orgAlpha)
	}
	if len(got) != 1 {
		t.Fatalf("ListAdmin returned %d rows, want 1", len(got))
	}
}

// The duplicate check must not consult another tenant's active requests: it
// both suppresses this tenant's request and hands the other tenant's row back.
func TestBloemDuplicateCheckIsBoundedToTheViewersOrganization(t *testing.T) {
	store := &boundedStore{fakeStore: newFakeStore()}
	svc := newBoundedService(store, map[int]uuid.UUID{5: orgAlpha})

	if _, err := svc.GetDetail(context.Background(), testViewer(5), MediaTypeMovie, 603); err != nil {
		t.Fatalf("GetDetail: %v", err)
	}
	if store.activeCalls == 0 {
		t.Fatal("duplicate check never reached the bounded query")
	}
	if store.activeOrg != orgAlpha {
		t.Fatalf("bounded duplicate check organization = %v, want %v", store.activeOrg, orgAlpha)
	}
	if store.unboundedHits != 0 {
		t.Fatalf("unbounded ListActiveByTMDB was reached %d times, want 0", store.unboundedHits)
	}
}

// Without a bounding store the service must still bound, via step 1's
// post-filter, rather than silently serving the unbounded result.
func TestBloemFallsBackToPostFilterWithoutABoundingStore(t *testing.T) {
	base := newFakeStore()
	store := &listAdminStore{fakeStore: base, rows: []*Request{
		{ID: "a", RequestedByUserID: 7, MediaType: MediaTypeMovie},
		{ID: "b", RequestedByUserID: 9, MediaType: MediaTypeMovie},
	}}
	svc := NewService(store, &fakeTMDBClient{}, &fakePresence{})
	svc.SetTenantScopeResolver(&fakeTenantScope{orgs: map[int]uuid.UUID{
		1: orgAlpha, 7: orgAlpha, 9: orgBeta,
	}})

	got, err := svc.ListAdmin(context.Background(), adminViewer(1), ListFilter{})
	if err != nil {
		t.Fatalf("ListAdmin: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("post-filter fallback returned %d rows, want only the alpha row", len(got))
	}
}

// *Repository is the production store; if it ever stops satisfying the optional
// capability the service silently degrades to post-filtering, which is safe but
// reintroduces short pages.
func TestBloemRepositorySatisfiesTheBoundingCapability(t *testing.T) {
	var _ tenantBoundedStore = (*Repository)(nil)
}

// *tenancy.Store must satisfy both halves of the resolver contract. Losing the
// optional half degrades requirePlatformAuthority to a no-op silently, which is
// how a server-wide control plane quietly reopens to every tenant.
func TestBloemTenancyStoreSatisfiesTheResolverContract(t *testing.T) {
	var store any = (*tenancy.Store)(nil)
	if _, ok := store.(TenantScopeResolver); !ok {
		t.Fatal("*tenancy.Store no longer satisfies TenantScopeResolver")
	}
	if _, ok := store.(defaultOrganizationResolver); !ok {
		t.Fatal("*tenancy.Store no longer satisfies defaultOrganizationResolver")
	}
}

// platformScope answers both halves of the resolver contract.
type platformScope struct {
	*fakeTenantScope
	defaultOrg uuid.UUID
	defaultErr error
}

func (p *platformScope) DefaultOrganization(context.Context) (tenancy.Organization, error) {
	if p.defaultErr != nil {
		return tenancy.Organization{}, p.defaultErr
	}
	return tenancy.Organization{ID: p.defaultOrg, Default: true}, nil
}

func newPlatformService(store *fakeStore, viewerOrg, defaultOrg uuid.UUID) *Service {
	svc := newTestService(store)
	svc.SetTenantScopeResolver(&platformScope{
		fakeTenantScope: &fakeTenantScope{orgs: map[int]uuid.UUID{1: viewerOrg}},
		defaultOrg:      defaultOrg,
	})
	return svc
}

// request_settings and request_integrations are server-global, so a tenant
// administrator must not reach them: integration writes repoint the operator's
// download clients and integration reads disclose their base URLs.
func TestBloemTenantAdminCannotReachServerGlobalSurfaces(t *testing.T) {
	cases := map[string]func(*Service, Viewer) error{
		"UpdateSettings": func(s *Service, v Viewer) error {
			_, err := s.UpdateSettings(context.Background(), v, Settings{GlobalMaxRequests: 1, GlobalWindowDays: 1})
			return err
		},
		"ListIntegrations": func(s *Service, v Viewer) error {
			_, err := s.ListIntegrations(context.Background(), v)
			return err
		},
		"CreateIntegration": func(s *Service, v Viewer) error {
			_, err := s.CreateIntegration(context.Background(), v, Integration{ID: "radarr"})
			return err
		},
		"DeleteIntegration": func(s *Service, v Viewer) error {
			return s.DeleteIntegration(context.Background(), v, "radarr")
		},
		"GetIntegration": func(s *Service, v Viewer) error {
			_, err := s.GetIntegration(context.Background(), v, "radarr")
			return err
		},
		"UpdateSettingsConditional": func(s *Service, v Viewer) error {
			_, err := s.UpdateSettingsConditional(context.Background(), v, Settings{GlobalMaxRequests: 1, GlobalWindowDays: 1}, 0)
			return err
		},
		"DeleteIntegrationConditional": func(s *Service, v Viewer) error {
			return s.DeleteIntegrationConditional(context.Background(), v, "radarr", 0)
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			svc := newPlatformService(newFakeStore(), orgBeta, orgAlpha) // tenant admin
			if err := call(svc, adminViewer(1)); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s as a tenant admin = %v, want ErrForbidden", name, err)
			}

			// The operator's own admin must still get through the guard.
			operator := newPlatformService(newFakeStore(), orgAlpha, orgAlpha)
			if err := call(operator, adminViewer(1)); errors.Is(err, ErrForbidden) {
				t.Fatalf("%s as the operator's admin was denied", name)
			}
		})
	}
}

// Tenant admins keep reading the settings that govern their own queue.
func TestBloemTenantAdminStillReadsSettings(t *testing.T) {
	svc := newPlatformService(newFakeStore(), orgBeta, orgAlpha)
	if _, err := svc.GetSettings(context.Background(), adminViewer(1)); err != nil {
		t.Fatalf("GetSettings as a tenant admin: %v", err)
	}
}

// A resolver that cannot name the operator organization must deny.
func TestBloemPlatformAuthorityFailsClosed(t *testing.T) {
	svc := newTestService(newFakeStore())
	svc.SetTenantScopeResolver(&platformScope{
		fakeTenantScope: &fakeTenantScope{orgs: map[int]uuid.UUID{1: orgAlpha}},
		defaultErr:      errors.New("tenant store down"),
	})
	if _, err := svc.ListIntegrations(context.Background(), adminViewer(1)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListIntegrations with a failing resolver = %v, want ErrForbidden", err)
	}
}
