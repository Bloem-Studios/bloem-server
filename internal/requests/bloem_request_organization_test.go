package requests

// Bloem-owned. A request belongs to the organization it was filed in, not to
// the requester's primary membership. The failure these tests pin: an account
// in organizations T (older) and E files a request while acting in E; under
// the old rule it was filed under T, so T's administrators could act on it and
// E's never saw it.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stampedStore records the organization each request was filed in, the way
// *Repository does.
type stampedStore struct {
	*fakeStore
	orgOf map[string]uuid.UUID
}

func (s *stampedStore) RequestOrganization(_ context.Context, id string) (uuid.UUID, error) {
	org, ok := s.orgOf[id]
	if !ok {
		return uuid.Nil, ErrNotFound
	}
	return org, nil
}

func actingIn(accountID int, organizationID uuid.UUID) context.Context {
	return tenancy.WithContext(context.Background(), tenancy.Context{AccountID: accountID, OrganizationID: organizationID})
}

func TestBloemRequestOrganizationIsTheStampedOneNotThePrimaryMembership(t *testing.T) {
	const requester, adminT, adminE = 7, 1, 2
	orgT, orgE := orgAlpha, orgBeta

	newSvc := func() *Service {
		base := newFakeStore()
		seedRequest(base, "req", requester)
		store := &stampedStore{fakeStore: base, orgOf: map[string]uuid.UUID{"req": orgE}}
		svc := NewService(store, &fakeTMDBClient{}, &fakePresence{})
		svc.SetUserRepository(requestUserRepo{})
		// The requester's primary membership is T; the request was filed in E.
		svc.SetTenantScopeResolver(&fakeTenantScope{orgs: map[int]uuid.UUID{requester: orgT, adminT: orgT, adminE: orgE}})
		return svc
	}

	if _, err := newSvc().GetRequest(actingIn(adminT, orgT), adminViewer(adminT), "req"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("T admin reading an E request = %v, want ErrForbidden", err)
	}
	if _, err := newSvc().Decline(actingIn(adminT, orgT), adminViewer(adminT), "req", "no"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("T admin declining an E request = %v, want ErrForbidden", err)
	}
	if _, err := newSvc().GetRequest(actingIn(adminE, orgE), adminViewer(adminE), "req"); err != nil {
		t.Fatalf("E admin reading an E request: %v", err)
	}
	if _, err := newSvc().Decline(actingIn(adminE, orgE), adminViewer(adminE), "req", "no"); err != nil {
		t.Fatalf("E admin declining an E request: %v", err)
	}
}

// Approving is organization authority: an administrator acting in T must not
// approve their own request filed in E.
func TestBloemAdminCannotApproveOwnRequestFiledInAnotherOrganization(t *testing.T) {
	base := newFakeStore()
	seedRequest(base, "req", 1)
	store := &stampedStore{fakeStore: base, orgOf: map[string]uuid.UUID{"req": orgBeta}}
	svc := NewService(store, &fakeTMDBClient{}, &fakePresence{})
	svc.SetTenantScopeResolver(&fakeTenantScope{orgs: map[int]uuid.UUID{1: orgAlpha}})

	if _, err := svc.Approve(actingIn(1, orgAlpha), adminViewer(1), "req"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Approve of own request filed elsewhere = %v, want ErrForbidden", err)
	}
	// Reading or cancelling it stays open to the requester.
	if _, err := svc.GetRequest(actingIn(1, orgAlpha), adminViewer(1), "req"); err != nil {
		t.Fatalf("requester reading own request: %v", err)
	}
}

func TestBloemRepositoryRecordsRequestOrganization(t *testing.T) {
	var _ requestOrganizationStore = (*Repository)(nil)
}

// newRequestOrgDisposableDatabase migrates a throwaway database and finalizes
// membership policy authority there, so memberships can be seeded without
// flipping that switch on the shared test database.
func newRequestOrgDisposableDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_reqorg_" + hex.EncodeToString(random[:])
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database: %v", err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		admin.Close()
	})
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}
	return pool
}

// requestOrgFixture seeds two organizations and an account that belongs to
// both, the T membership older, in a disposable migrated database.
type requestOrgFixture struct {
	pool        *pgxpool.Pool
	orgT, orgE  uuid.UUID
	accountID   int
	singleOrgID int
}

func newRequestOrgFixture(t *testing.T) requestOrgFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newRequestOrgDisposableDatabase(t, ctx, dsn)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	f := requestOrgFixture{pool: pool}

	insertOrg := func(label string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO organizations (slug, name, status, is_default)
			VALUES ($1, $1, 'initializing', false) RETURNING id`, "reqorg-"+label+"-"+suffix).Scan(&id); err != nil {
			t.Fatalf("insert organization %s: %v", label, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, id) })
		return id
	}
	insertAccount := func(label string) int {
		var id int
		name := "reqorg-" + label + "-" + suffix
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (username, email, password_hash, role, enabled)
			VALUES ($1, $2, 'x', 'user', true) RETURNING id`, name, name+"@example.test").Scan(&id); err != nil {
			t.Fatalf("insert account %s: %v", label, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	insertMembership := func(org uuid.UUID, account int, age string) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role, created_at)
			SELECT $1, $2, 'active', 'user', now() - $3::interval
			WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, org, account, age); err != nil {
			t.Fatalf("insert membership: %v", err)
		}
	}
	f.orgT = insertOrg("t")
	f.orgE = insertOrg("e")
	f.accountID = insertAccount("multi")
	f.singleOrgID = insertAccount("single")
	insertMembership(f.orgT, f.accountID, "2 days")
	insertMembership(f.orgE, f.accountID, "1 day")
	insertMembership(f.orgE, f.singleOrgID, "1 day")
	return f
}

func (f requestOrgFixture) create(t *testing.T, ctx context.Context, repo *Repository, accountID, tmdbID int) *Request {
	t.Helper()
	id := fmt.Sprintf("reqorg-%d-%d", time.Now().UnixNano(), tmdbID)
	req, err := repo.CreateRequest(ctx, CreateRequestRecord{
		ID:        id,
		Input:     CreateRequestInput{MediaType: MediaTypeMovie, TMDBID: tmdbID, Title: "Fixture"},
		Requester: Viewer{UserID: accountID, ProfileID: "p"},
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	return req
}

func TestBloemRequestFiledInTheActingOrganizationDatabase(t *testing.T) {
	f := newRequestOrgFixture(t)
	repo := NewRepository(f.pool, nil)
	base := int(time.Now().UnixNano()%1_000_000_000) + 1

	// Acting in E, the younger membership: filed in E.
	acting := tenancy.WithContext(context.Background(), tenancy.Context{AccountID: f.accountID, OrganizationID: f.orgE})
	inE := f.create(t, acting, repo, f.accountID, base)
	if got, err := repo.RequestOrganization(context.Background(), inE.ID); err != nil || got != f.orgE {
		t.Fatalf("request filed acting in E recorded under %v (%v), want E %v", got, err, f.orgE)
	}

	// No tenant (outside an HTTP request): the primary membership, T.
	noTenant := f.create(t, context.Background(), repo, f.accountID, base+1)
	if got, err := repo.RequestOrganization(context.Background(), noTenant.ID); err != nil || got != f.orgT {
		t.Fatalf("request filed without a tenant recorded under %v (%v), want T %v", got, err, f.orgT)
	}

	// A tenant resolved for another account is a wiring fault.
	wrong := tenancy.WithContext(context.Background(), tenancy.Context{AccountID: f.singleOrgID, OrganizationID: f.orgE})
	if _, err := repo.CreateRequest(wrong, CreateRequestRecord{
		ID:        fmt.Sprintf("reqorg-wrong-%d", base),
		Input:     CreateRequestInput{MediaType: MediaTypeMovie, TMDBID: base + 2, Title: "Fixture"},
		Requester: Viewer{UserID: f.accountID, ProfileID: "p"},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateRequest with another account's tenant = %v, want ErrForbidden", err)
	}

	// The admin queue is bounded by the stamped column.
	listE, err := repo.ListAdminInOrganization(context.Background(), f.orgE, ListFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	listT, err := repo.ListAdminInOrganization(context.Background(), f.orgT, ListFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	has := func(reqs []*Request, id string) bool {
		for _, r := range reqs {
			if r.ID == id {
				return true
			}
		}
		return false
	}
	if !has(listE, inE.ID) || has(listT, inE.ID) {
		t.Fatalf("E request: in E queue=%v, in T queue=%v; want true,false", has(listE, inE.ID), has(listT, inE.ID))
	}
	if !has(listT, noTenant.ID) || has(listE, noTenant.ID) {
		t.Fatalf("T request: in T queue=%v, in E queue=%v; want true,false", has(listT, noTenant.ID), has(listE, noTenant.ID))
	}

	// Duplicate check and failed-row cleanup follow the column too.
	activeT, err := repo.ListActiveByTMDBInOrganization(context.Background(), f.orgT, MediaTypeMovie, []int{base})
	if err != nil {
		t.Fatal(err)
	}
	if activeT[base] != nil {
		t.Fatal("T's duplicate check saw the request filed in E")
	}
	activeE, err := repo.ListActiveByTMDBInOrganization(context.Background(), f.orgE, MediaTypeMovie, []int{base})
	if err != nil {
		t.Fatal(err)
	}
	if activeE[base] == nil || activeE[base].ID != inE.ID {
		t.Fatal("E's duplicate check missed the request filed in E")
	}

	// End to end through the service: the E administrator can act on the
	// request; the T administrator cannot. Viewer ids are only compared with
	// the tenant context here, so they need no rows.
	svc := NewService(repo, &fakeTMDBClient{}, &fakePresence{})
	svc.SetTenantScopeResolver(tenancy.NewStore(f.pool))
	const adminT, adminE = -101, -102
	asT := tenancy.WithContext(context.Background(), tenancy.Context{AccountID: adminT, OrganizationID: f.orgT})
	asE := tenancy.WithContext(context.Background(), tenancy.Context{AccountID: adminE, OrganizationID: f.orgE})
	if _, err := svc.Decline(asT, adminViewer(adminT), inE.ID, "no"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("T admin Decline of the E request = %v, want ErrForbidden", err)
	}
	if _, err := svc.Decline(asE, adminViewer(adminE), noTenant.ID, "no"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("E admin Decline of the T request = %v, want ErrForbidden", err)
	}
	if _, err := svc.Decline(asE, adminViewer(adminE), inE.ID, "no"); err != nil {
		t.Fatalf("E admin Decline of the E request: %v", err)
	}
}
