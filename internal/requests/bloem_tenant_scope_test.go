package requests

// Bloem-owned. Covers the organization bound added in bloem_tenant_scope.go.
// Reuses Silo's fakeStore, newTestService and testViewer from service_test.go
// rather than editing that file: same package, so unexported helpers are
// reachable from a file Bloem owns outright.

import (
	"context"
	"errors"
	"testing"

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
