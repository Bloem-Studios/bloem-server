package access

// Bloem organization-scoped group store coverage and the package's disposable
// database TestMain, moved out of Silo's group_store_test.go.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

var accessGroupTestDatabaseConfig *pgxpool.Config

func TestMain(m *testing.M) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	var cleanup func() error
	if dsn == "" && os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
		_, _ = fmt.Fprintln(os.Stderr, "SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		os.Exit(1)
	}
	if dsn != "" {
		var err error
		accessGroupTestDatabaseConfig, cleanup, err = prepareAccessGroupTestDatabase(context.Background(), dsn)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "prepare access-group test database: %v\n", err)
			os.Exit(1)
		}
	}

	code := m.Run()
	if cleanup != nil {
		if err := cleanup(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "clean up access-group test database: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

func TestGroupStoreNeverReadsOrMutatesAnotherOrganization(t *testing.T) {
	ctx, fixture := newOrganizationGroupStoreDBTest(t)
	local := fixture.createGroup(fixture.orgA, "Shared Name")
	foreign := fixture.createGroup(fixture.orgB, "Shared Name")
	foreignDefault := fixture.createDefaultGroup(fixture.orgB, "Default B")
	localDefaultID := defaultAccessGroupSeedID(t, ctx, fixture.pool, fixture.orgA)
	assertSingleDefaultGroup(t, ctx, fixture.pool, fixture.orgA, localDefaultID)
	assertSingleDefaultGroup(t, ctx, fixture.pool, fixture.orgB, foreignDefault.ID)

	if _, err := fixture.store.Get(ctx, fixture.orgA, foreign.ID); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("foreign Get error = %v, want ErrGroupNotFound", err)
	}
	changed := "Changed"
	if _, err := fixture.store.Update(ctx, fixture.orgA, foreign.ID, UpdateGroupInput{Name: &changed}); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("foreign Update error = %v, want ErrGroupNotFound", err)
	}
	if err := fixture.store.Delete(ctx, fixture.orgA, foreign.ID); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("foreign Delete error = %v, want ErrGroupNotFound", err)
	}

	groups, err := fixture.store.List(ctx, fixture.orgA)
	if err != nil {
		t.Fatalf("List(org A) error: %v", err)
	}
	if len(groups) != 2 { // seeded default plus the local group
		t.Fatalf("List(org A) returned %d groups, want 2", len(groups))
	}
	for _, group := range groups {
		if group.OrganizationID != fixture.orgA || group.ID == foreign.ID {
			t.Fatalf("List(org A) disclosed foreign group: %#v", group)
		}
	}

	gotForeign, err := fixture.store.Get(ctx, fixture.orgB, foreign.ID)
	if err != nil {
		t.Fatalf("Get(org B foreign) error: %v", err)
	}
	if gotForeign.Name != "Shared Name" {
		t.Fatalf("foreign group name = %q, want unchanged", gotForeign.Name)
	}
	if local.Name != foreign.Name {
		t.Fatalf("same-name groups were not created: local=%q foreign=%q", local.Name, foreign.Name)
	}

	deletable := fixture.createGroup(fixture.orgA, "Delete Me")
	localAccountID := fixture.createUser(&deletable.ID, 1)
	localProfileID := fixture.createProfile(localAccountID, fixture.orgA, &deletable.ID)
	foreignAccountID := fixture.createUser(&foreign.ID, 1)
	foreignProfileID := fixture.createProfile(foreignAccountID, fixture.orgB, &foreign.ID)
	if err := fixture.store.Delete(ctx, fixture.orgA, deletable.ID); err != nil {
		t.Fatalf("delete organization A group: %v", err)
	}
	assertProfileAccessGroup(t, ctx, fixture.pool, localAccountID, localProfileID, localDefaultID)
	assertProfileAccessGroup(t, ctx, fixture.pool, foreignAccountID, foreignProfileID, foreign.ID)

	isDefault := true
	if _, err := fixture.store.Update(ctx, fixture.orgA, local.ID, UpdateGroupInput{IsDefault: &isDefault}); err != nil {
		t.Fatalf("promote local default: %v", err)
	}
	assertSingleDefaultGroup(t, ctx, fixture.pool, fixture.orgA, local.ID)
	assertSingleDefaultGroup(t, ctx, fixture.pool, fixture.orgB, foreignDefault.ID)
}

func TestGroupStoreMemberCountsUseProfilesNotLegacyAccounts(t *testing.T) {
	ctx, fixture := newOrganizationGroupStoreDBTest(t)
	group := fixture.createGroup(fixture.orgA, "Profile Members")
	canonicalAccountID := fixture.createUser(nil, 1)
	fixture.createProfile(canonicalAccountID, fixture.orgA, &group.ID)
	fixture.createProfile(canonicalAccountID, fixture.orgA, &group.ID)
	legacyOnlyAccountID := fixture.createUser(&group.ID, 1)
	fixture.createProfile(legacyOnlyAccountID, fixture.orgA, nil)

	got, err := fixture.store.Get(ctx, fixture.orgA, group.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.MemberCount != 2 {
		t.Fatalf("member_count = %d, want two assigned profiles", got.MemberCount)
	}
}

func TestGroupStoreRejectsCohortManagedMutationAndDeletion(t *testing.T) {
	ctx, fixture := newOrganizationGroupStoreDBTest(t)
	group := fixture.createGroup(fixture.orgA, "Cohort managed")
	actorID := fixture.createUser(&group.ID, 1)
	cohortID := uuid.New()
	cohortRevisionID := uuid.New()
	_, err := fixture.pool.Exec(ctx, `
		INSERT INTO entitlement_policy_cohorts (id,organization_id,name)
		VALUES ($1,$2,'Cohort managed')`, cohortID, fixture.orgA)
	if err != nil {
		t.Fatalf("insert cohort identity: %v", err)
	}
	_, err = fixture.pool.Exec(ctx, `
		UPDATE access_groups
		SET managed_template_key='standard',managed_template_revision=1,
		    library_ids=ARRAY[]::integer[],playback_allowed=true,max_streams=2,
		    max_profiles=2,transcode_allowed=true,max_transcodes=1,
		    download_allowed=true,download_transcode_allowed=true,
		    max_playback_quality='1080p',allowed_permissions=ARRAY['marker_edit']::text[],
		    requests_allowed=true
		WHERE id=$1`, group.ID)
	if err != nil {
		t.Fatalf("prepare cohort group policy: %v", err)
	}
	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin cohort marker transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO entitlement_policy_cohort_revisions (
			id,cohort_id,organization_id,name,revision,access_group_id,
			source_template_key,source_template_revision,derivation_kind,
			library_ids,playback_allowed,max_streams,max_profiles,
			transcode_allowed,max_transcodes,download_allowed,
			download_transcode_allowed,max_playback_quality,
			allowed_permissions,requests_allowed,policy_digest,created_by_account_id
		) VALUES (
			$1,$2,$3,'Cohort managed',1,$4,
			'standard',1,'exact_template',
			ARRAY[]::integer[],true,2,2,true,1,true,true,'1080p',
			ARRAY['marker_edit']::text[],true,repeat('a',64),$5
		)`, cohortRevisionID, cohortID, fixture.orgA, group.ID, actorID)
	if err != nil {
		t.Fatalf("insert cohort revision: %v", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE access_groups
		SET managed_cohort_id=$2
		WHERE id=$1`, group.ID, cohortRevisionID)
	if err != nil {
		t.Fatalf("mark cohort group: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit cohort marker: %v", err)
	}

	name := "mutated"
	if _, err := fixture.store.Update(ctx, fixture.orgA, group.ID, UpdateGroupInput{Name: &name}); !errors.Is(err, ErrManagedGroup) {
		t.Fatalf("Update() error = %v, want ErrManagedGroup", err)
	}
	if err := fixture.store.Delete(ctx, fixture.orgA, group.ID); !errors.Is(err, ErrManagedGroup) {
		t.Fatalf("Delete() error = %v, want ErrManagedGroup", err)
	}
}

func TestGroupStoreResolvePolicyRejectsForeignOrMismatchedProfile(t *testing.T) {
	ctx, fixture := newOrganizationGroupStoreDBTest(t)
	groupA := fixture.createGroup(fixture.orgA, "Policy A")
	groupB := fixture.createGroup(fixture.orgB, "Policy B")
	accountA := fixture.createUser(&groupA.ID, 1)
	accountB := fixture.createUser(&groupB.ID, 1)
	profileA := fixture.createProfile(accountA, fixture.orgA, &groupA.ID)
	profileB := fixture.createProfile(accountB, fixture.orgB, &groupB.ID)
	defaultGroupID := defaultAccessGroupSeedID(t, ctx, fixture.pool, fixture.orgA)
	defaultProfile := fixture.createProfile(accountA, fixture.orgA, nil)

	policy, err := fixture.store.ResolvePolicy(ctx, GroupSubject{
		OrganizationID: fixture.orgA,
		AccountID:      accountA,
		ProfileID:      profileA,
	})
	if err != nil || policy == nil || policy.ID != groupA.ID {
		t.Fatalf("ResolvePolicy(local) = %#v, %v; want group %d", policy, err, groupA.ID)
	}

	for name, subject := range map[string]GroupSubject{
		"foreign organization": {
			OrganizationID: fixture.orgA,
			AccountID:      accountB,
			ProfileID:      profileB,
		},
		"foreign account": {
			OrganizationID: fixture.orgA,
			AccountID:      accountB,
			ProfileID:      profileA,
		},
		"v2 without profile": {
			OrganizationID: fixture.orgA,
			AccountID:      accountA,
		},
		"legacy outside default organization": {
			OrganizationID: fixture.orgB,
			AccountID:      accountB,
			Legacy:         true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fixture.store.ResolvePolicy(ctx, subject); !errors.Is(err, ErrGroupNotFound) {
				t.Fatalf("ResolvePolicy() error = %v, want ErrGroupNotFound", err)
			}
		})
	}

	policy, err = fixture.store.ResolvePolicy(ctx, GroupSubject{
		OrganizationID: fixture.orgA,
		AccountID:      accountA,
		ProfileID:      defaultProfile,
	})
	if err != nil || policy == nil || policy.ID != defaultGroupID {
		t.Fatalf("ResolvePolicy(default-assigned profile) = %#v, %v; want group %d", policy, err, defaultGroupID)
	}

	policy, err = fixture.store.ResolvePolicy(ctx, GroupSubject{
		OrganizationID: fixture.orgA,
		AccountID:      accountA,
		Legacy:         true,
	})
	if err != nil || policy == nil || policy.ID != groupA.ID {
		t.Fatalf("ResolvePolicy(default-org legacy ceiling) = %#v, %v; want group %d", policy, err, groupA.ID)
	}
}

func prepareAccessGroupTestDatabase(ctx context.Context, dsn string) (*pgxpool.Config, func() error, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, fmt.Errorf("generate disposable database name: %w", err)
	}
	name := "bloem_access_groups_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("parse maintenance database URL: %w", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("connect maintenance database: %w", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		return nil, nil, fmt.Errorf("create disposable database %q: %w", name, err)
	}
	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		return nil, nil, fmt.Errorf("parse disposable database URL: %w", err)
	}
	testConfig.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		return nil, nil, fmt.Errorf("connect disposable database: %w", err)
	}
	if err := migrateAccessGroupDisposableDatabase(ctx, pool); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		return nil, nil, fmt.Errorf("migrate disposable database: %w", err)
	}
	// 20260829085838_membership_policy_isolation leaves a freshly migrated
	// database in the compatibility phase, which is a policy FREEZE: writes are
	// fenced on users and frozen on organization_memberships. These tests
	// exercise policy mutation, so they run in the steady state the handover
	// produces rather than mid-freeze.
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		return nil, nil, fmt.Errorf("finalize membership policy authority: %w", err)
	}
	pool.Close()
	cleanup := func() error {
		defer admin.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			return fmt.Errorf("drop disposable database %q: %w", name, err)
		}
		var exists bool
		if err := admin.QueryRow(cleanupCtx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, name).Scan(&exists); err != nil {
			return fmt.Errorf("verify disposable database %q cleanup: %w", name, err)
		}
		if exists {
			return fmt.Errorf("disposable database %q still exists after cleanup", name)
		}
		return nil
	}
	return testConfig.Copy(), cleanup, nil
}

func migrateAccessGroupDisposableDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	migrationFS, err := fs.Sub(migrations.FS, "sql")
	if err != nil {
		return fmt.Errorf("open embedded SQL migrations: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		stdlib.OpenDBFromPool(pool),
		migrationFS,
		goose.WithTableName("public.goose_db_version"),
		goose.WithAllowOutofOrder(true),
	)
	if err != nil {
		return fmt.Errorf("create Goose provider: %w", err)
	}
	defer func() { _ = provider.Close() }()
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply embedded SQL migrations: %w", err)
	}
	return nil
}

func defaultAccessGroupOrganizationID(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&id); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	return id
}

type organizationGroupStoreDBFixture struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	store  *GroupStore
	suffix string
	orgA   uuid.UUID
	orgB   uuid.UUID
	groups []int64
	seedID int64
}

func newOrganizationGroupStoreDBTest(t *testing.T) (context.Context, *organizationGroupStoreDBFixture) {
	t.Helper()
	ctx, pool, store, suffix, orgA := newGroupStoreDBTest(t)
	fixture := &organizationGroupStoreDBFixture{
		t:      t,
		ctx:    ctx,
		pool:   pool,
		store:  store,
		suffix: suffix,
		orgA:   orgA,
		seedID: defaultAccessGroupSeedID(t, ctx, pool, orgA),
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status)
		VALUES ($1, $2, 'initializing')
		RETURNING id`,
		"access-group-test-"+suffix,
		"Access Group Test "+suffix,
	).Scan(&fixture.orgB); err != nil {
		t.Fatalf("create second organization: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE username LIKE $1`, "access-group-test-"+suffix+"%")
		if len(fixture.groups) > 0 {
			_, _ = pool.Exec(ctx, `DELETE FROM access_groups WHERE id = ANY($1)`, fixture.groups)
		}
		_, _ = pool.Exec(ctx, `UPDATE access_groups SET is_default = true WHERE organization_id = $1 AND id = $2`, fixture.orgA, fixture.seedID)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, fixture.orgB)
	})
	return ctx, fixture
}

func (f *organizationGroupStoreDBFixture) createGroup(organizationID uuid.UUID, name string) *Group {
	f.t.Helper()
	group, err := f.store.Create(f.ctx, organizationID, CreateGroupInput{
		Name:                     name,
		LibraryIDs:               []int{1, 3},
		MaxPlaybackQuality:       PlaybackQualityStandard,
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: true,
		MaxStreams:               2,
		MaxTranscodes:            1,
		AllowedPermissions:       []string{"marker_edit"},
		RequestsAllowed:          true,
	})
	if err != nil {
		f.t.Fatalf("create group %q in %s: %v", name, organizationID, err)
	}
	f.groups = append(f.groups, group.ID)
	return group
}

func (f *organizationGroupStoreDBFixture) createDefaultGroup(organizationID uuid.UUID, name string) *Group {
	f.t.Helper()
	group, err := f.store.Create(f.ctx, organizationID, CreateGroupInput{
		Name:                     name,
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: true,
		RequestsAllowed:          true,
		IsDefault:                true,
	})
	if err != nil {
		f.t.Fatalf("create default group %q in %s: %v", name, organizationID, err)
	}
	f.groups = append(f.groups, group.ID)
	return group
}

func (f *organizationGroupStoreDBFixture) createUser(legacyGroupID *int64, revision int64) int {
	f.t.Helper()
	return insertAccessGroupTestUser(f.t, f.ctx, f.pool, f.suffix, legacyGroupID, revision)
}

func (f *organizationGroupStoreDBFixture) createProfile(accountID int, organizationID uuid.UUID, groupID *int64) string {
	f.t.Helper()
	return insertAccessGroupTestProfile(f.t, f.ctx, f.pool, f.suffix, accountID, organizationID, groupID)
}

func insertAccessGroupTestProfile(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	suffix string,
	userID int,
	organizationID uuid.UUID,
	groupID *int64,
) string {
	t.Helper()
	if groupID == nil {
		var defaultGroupID int64
		if err := pool.QueryRow(ctx, `
			SELECT id
			FROM access_groups
			WHERE organization_id = $1 AND is_default`, organizationID).Scan(&defaultGroupID); err != nil {
			t.Fatalf("load canonical default group for test profile: %v", err)
		}
		groupID = &defaultGroupID
	}
	profileID := fmt.Sprintf("access-group-test-%s-%d", suffix, time.Now().UnixNano())
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name, organization_id, access_group_id)
		VALUES ($1, $2, $3, $4, $5)`,
		profileID,
		userID,
		profileID,
		organizationID,
		groupID,
	); err != nil {
		t.Fatalf("insert test profile: %v", err)
	}
	return profileID
}

func assertProfileAccessGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int, profileID string, want int64) {
	t.Helper()
	var got int64
	if err := pool.QueryRow(ctx, `
		SELECT access_group_id
		FROM user_profiles
		WHERE user_id = $1 AND id = $2`, userID, profileID).Scan(&got); err != nil {
		t.Fatalf("load profile access group: %v", err)
	}
	if got != want {
		t.Fatalf("profile access group = %d, want %d", got, want)
	}
}
