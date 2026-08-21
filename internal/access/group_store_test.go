package access

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func TestGroupStoreCRUDAndMemberCountsDB(t *testing.T) {
	ctx, pool, store, suffix, organizationID := newGroupStoreDBTest(t)
	group := createTestGroup(t, ctx, store, organizationID, suffix, "crud")
	firstUserID := insertAccessGroupTestUser(t, ctx, pool, suffix, &group.ID, 1)
	secondUserID := insertAccessGroupTestUser(t, ctx, pool, suffix, &group.ID, 2)
	insertAccessGroupTestProfile(t, ctx, pool, suffix, firstUserID, organizationID, &group.ID)
	insertAccessGroupTestProfile(t, ctx, pool, suffix, secondUserID, organizationID, &group.ID)

	got, err := store.Get(ctx, organizationID, group.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.MemberCount != 2 {
		t.Fatalf("member_count = %d, want 2", got.MemberCount)
	}
	if !reflect.DeepEqual(got.LibraryIDs, []int{1, 3}) {
		t.Fatalf("library_ids = %#v, want [1 3]", got.LibraryIDs)
	}

	groups, err := store.List(ctx, organizationID)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	found := false
	for _, listed := range groups {
		if listed.ID == group.ID {
			found = true
			if listed.MemberCount != 2 {
				t.Fatalf("listed member_count = %d, want 2", listed.MemberCount)
			}
		}
	}
	if !found {
		t.Fatalf("created group %d not found in List()", group.ID)
	}

	description := "updated"
	maxStreams := 1
	updated, err := store.Update(ctx, organizationID, group.ID, UpdateGroupInput{
		Description: &description,
		MaxStreams:  &maxStreams,
	})
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	if updated.Description != "updated" || updated.MaxStreams != 1 {
		t.Fatalf("updated group = %#v, want description/max_streams update", updated)
	}

	impact, err := store.DeleteWithImpact(ctx, organizationID, group.ID)
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	var assigned int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM users
		WHERE username LIKE $1 AND access_group_id IS NOT NULL`,
		"access-group-test-"+suffix+"%",
	).Scan(&assigned); err != nil {
		t.Fatalf("count assigned users after delete: %v", err)
	}
	if assigned != 0 {
		t.Fatalf("assigned users after delete = %d, want 0", assigned)
	}
	defaultGroupID := defaultAccessGroupSeedID(t, ctx, pool, organizationID)
	var reassigned int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::int
		FROM user_profiles
		WHERE user_id = ANY($1)
		  AND organization_id = $2
		  AND access_group_id = $3`, []int{firstUserID, secondUserID}, organizationID, defaultGroupID).Scan(&reassigned); err != nil {
		t.Fatalf("count reassigned profiles after delete: %v", err)
	}
	if reassigned != 2 {
		t.Fatalf("profiles reassigned to default after delete = %d, want 2", reassigned)
	}
	if impact.ProfilesReassigned != 2 || impact.DefaultGroupID != defaultGroupID {
		t.Fatalf("deletion impact = %+v, want two profiles and default group %d", impact, defaultGroupID)
	}
}

func TestGroupStoreResolvePolicyDB(t *testing.T) {
	ctx, pool, store, suffix, organizationID := newGroupStoreDBTest(t)
	group := createTestGroup(t, ctx, store, organizationID, suffix, "policy")
	defaultGroupID := defaultAccessGroupSeedID(t, ctx, pool, organizationID)
	memberID := insertAccessGroupTestUser(t, ctx, pool, suffix, &group.ID, 1)
	noGroupID := insertAccessGroupTestUser(t, ctx, pool, suffix, nil, 1)
	memberProfileID := insertAccessGroupTestProfile(t, ctx, pool, suffix, memberID, organizationID, &group.ID)
	noGroupProfileID := insertAccessGroupTestProfile(t, ctx, pool, suffix, noGroupID, organizationID, nil)

	policy, err := store.ResolvePolicy(ctx, GroupSubject{
		OrganizationID: organizationID,
		AccountID:      memberID,
		ProfileID:      memberProfileID,
	})
	if err != nil {
		t.Fatalf("ResolvePolicy(member) error: %v", err)
	}
	if policy == nil || policy.ID != group.ID || !reflect.DeepEqual(policy.LibraryIDs, []int{1, 3}) ||
		!policy.PlaybackAllowed || policy.TranscodeAllowed || !policy.AudioTranscodeAllowed || policy.MaxProfiles != 2 {
		t.Fatalf("policy = %#v, want group policy", policy)
	}
	if policy.TranscodeAllowed || !policy.AudioTranscodeAllowed {
		t.Fatalf("policy transcode gates = %t/%t, want false/true", policy.TranscodeAllowed, policy.AudioTranscodeAllowed)
	}
	if !reflect.DeepEqual(group.Policy(), *policy) {
		t.Fatalf("Group.Policy() = %#v, want ResolvePolicy %#v", group.Policy(), *policy)
	}
	transcodeAllowed := true
	if _, err := store.Update(ctx, organizationID, group.ID, UpdateGroupInput{TranscodeAllowed: &transcodeAllowed}); err != nil {
		t.Fatalf("Update(transcode_allowed) error: %v", err)
	}
	policy, err = store.ResolvePolicy(ctx, GroupSubject{
		OrganizationID: organizationID,
		AccountID:      memberID,
		ProfileID:      memberProfileID,
	})
	if err != nil {
		t.Fatalf("ResolvePolicy(after update) error: %v", err)
	}
	if policy == nil || !policy.TranscodeAllowed {
		t.Fatalf("policy after update = %#v, want transcode_allowed true", policy)
	}
	accountGroup, err := store.GetForAccount(ctx, memberID, group.ID)
	if err != nil || accountGroup.ID != group.ID || accountGroup.MaxProfiles != 2 {
		t.Fatalf("GetForAccount() = %#v, %v; want group %d", accountGroup, err, group.ID)
	}
	policy, err = store.ResolvePolicy(ctx, GroupSubject{
		OrganizationID: organizationID,
		AccountID:      noGroupID,
		ProfileID:      noGroupProfileID,
	})
	if err != nil {
		t.Fatalf("ResolvePolicy(no group) error: %v", err)
	}
	if policy == nil || policy.ID != defaultGroupID {
		t.Fatalf("policy = %#v, want canonical default group %d", policy, defaultGroupID)
	}
}

func TestGroupStoreAuthorizationUpdatesBumpMemberRevisionsDB(t *testing.T) {
	ctx, pool, store, suffix, organizationID := newGroupStoreDBTest(t)
	group := createTestGroup(t, ctx, store, organizationID, suffix, "quality")
	memberID := insertAccessGroupTestUser(t, ctx, pool, suffix, nil, 10)
	legacyOnlyID := insertAccessGroupTestUser(t, ctx, pool, suffix, &group.ID, 30)
	nonMemberID := insertAccessGroupTestUser(t, ctx, pool, suffix, nil, 20)
	insertAccessGroupTestProfile(t, ctx, pool, suffix, memberID, organizationID, &group.ID)
	insertAccessGroupTestProfile(t, ctx, pool, suffix, memberID, organizationID, &group.ID)
	insertAccessGroupTestProfile(t, ctx, pool, suffix, legacyOnlyID, organizationID, nil)
	insertAccessGroupTestProfile(t, ctx, pool, suffix, nonMemberID, organizationID, nil)

	intPtr := func(value int) *int { return &value }
	boolPtr := func(value bool) *bool { return &value }
	stringPtr := func(value string) *string { return &value }
	intSlicePtr := func(value []int) *[]int { return &value }
	stringSlicePtr := func(value []string) *[]string { return &value }
	tests := []struct {
		name  string
		input UpdateGroupInput
	}{
		{name: "libraries", input: UpdateGroupInput{LibraryIDs: intSlicePtr([]int{2})}},
		{name: "max quality", input: UpdateGroupInput{MaxPlaybackQuality: stringPtr(PlaybackQualityStandard)}},
		{name: "downloads", input: UpdateGroupInput{DownloadAllowed: boolPtr(false)}},
		{name: "download transcode", input: UpdateGroupInput{DownloadTranscodeAllowed: boolPtr(false)}},
		{name: "max streams", input: UpdateGroupInput{MaxStreams: intPtr(1)}},
		{name: "max transcodes", input: UpdateGroupInput{MaxTranscodes: intPtr(1)}},
		{name: "permissions", input: UpdateGroupInput{AllowedPermissions: stringSlicePtr([]string{"request"})}},
		{name: "requests", input: UpdateGroupInput{RequestsAllowed: boolPtr(false)}},
	}
	wantRevision := int64(10)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.Update(ctx, organizationID, group.ID, test.input); err != nil {
				t.Fatalf("Update() error: %v", err)
			}
			wantRevision++
			if got := accessPolicyRevisionForUser(t, ctx, pool, memberID); got != wantRevision {
				t.Fatalf("member revision = %d, want %d", got, wantRevision)
			}
			if _, err := store.Update(ctx, organizationID, group.ID, test.input); err != nil {
				t.Fatalf("no-op Update() error: %v", err)
			}
			if got := accessPolicyRevisionForUser(t, ctx, pool, memberID); got != wantRevision {
				t.Fatalf("member revision after no-op = %d, want %d", got, wantRevision)
			}
		})
	}
	description := "no revision bump"
	if _, err := store.Update(ctx, organizationID, group.ID, UpdateGroupInput{Description: &description}); err != nil {
		t.Fatalf("Update(description) error: %v", err)
	}
	if got := accessPolicyRevisionForUser(t, ctx, pool, memberID); got != wantRevision {
		t.Fatalf("member revision after description update = %d, want %d", got, wantRevision)
	}
	if _, err := store.Update(ctx, organizationID, group.ID, UpdateGroupInput{}); err != nil {
		t.Fatalf("empty Update() error: %v", err)
	}
	if got := accessPolicyRevisionForUser(t, ctx, pool, memberID); got != wantRevision {
		t.Fatalf("member revision after empty update = %d, want %d", got, wantRevision)
	}
	if got := accessPolicyRevisionForUser(t, ctx, pool, nonMemberID); got != 20 {
		t.Fatalf("non-member revision after quality update = %d, want 20", got)
	}
	if got := accessPolicyRevisionForUser(t, ctx, pool, legacyOnlyID); got != 30 {
		t.Fatalf("legacy-only account revision after quality update = %d, want 30", got)
	}
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

func TestDefaultAccessGroupSeedAndUniqueDB(t *testing.T) {
	ctx, pool, store, suffix, organizationID := newGroupStoreDBTest(t)
	seedID := defaultAccessGroupSeedID(t, ctx, pool, organizationID)
	t.Cleanup(func() {
		restoreDefaultAccessGroup(t, ctx, pool, organizationID, seedID)
	})

	assertDefaultGroupSeed(t, ctx, pool, organizationID)

	_, err := pool.Exec(ctx, `
		INSERT INTO access_groups (name, is_default, organization_id)
		VALUES ($1, true, $2)`,
		"Access Group Test "+suffix+" second default",
		organizationID,
	)
	if err == nil {
		t.Fatal("second default access group insert succeeded, want unique violation")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second default insert error = %v, want unique violation", err)
	}

	group := createTestGroup(t, ctx, store, organizationID, suffix, "swap-default")
	isDefault := true
	updated, err := store.Update(ctx, organizationID, group.ID, UpdateGroupInput{IsDefault: &isDefault})
	if err != nil {
		t.Fatalf("Update(is_default true) error: %v", err)
	}
	if !updated.IsDefault {
		t.Fatalf("updated IsDefault = false, want true")
	}
	assertSingleDefaultGroup(t, ctx, pool, organizationID, group.ID)

	isDefault = false
	if _, err := store.Update(ctx, organizationID, group.ID, UpdateGroupInput{IsDefault: &isDefault}); !errors.Is(err, ErrDefaultGroupRequired) {
		t.Fatalf("Update(is_default false) on the default group error = %v, want ErrDefaultGroupRequired", err)
	}
	assertSingleDefaultGroup(t, ctx, pool, organizationID, group.ID)
}

func TestGroupStoreDeleteDefaultRejectedDB(t *testing.T) {
	ctx, pool, store, suffix, organizationID := newGroupStoreDBTest(t)
	seedID := defaultAccessGroupSeedID(t, ctx, pool, organizationID)
	t.Cleanup(func() {
		restoreDefaultAccessGroup(t, ctx, pool, organizationID, seedID)
	})

	group := createTestGroup(t, ctx, store, organizationID, suffix, "delete-default")
	isDefault := true
	if _, err := store.Update(ctx, organizationID, group.ID, UpdateGroupInput{IsDefault: &isDefault}); err != nil {
		t.Fatalf("Update(is_default true) error: %v", err)
	}
	userID := insertAccessGroupTestUser(t, ctx, pool, suffix, &group.ID, 1)

	if err := store.Delete(ctx, organizationID, group.ID); !errors.Is(err, ErrDefaultGroupRequired) {
		t.Fatalf("Delete(default) error = %v, want ErrDefaultGroupRequired", err)
	}
	var hasGroup bool
	if err := pool.QueryRow(ctx, `
		SELECT access_group_id IS NOT NULL
		FROM users
		WHERE id = $1`, userID).Scan(&hasGroup); err != nil {
		t.Fatalf("load default group member: %v", err)
	}
	if !hasGroup {
		t.Fatalf("user access_group_id cleared by rejected default-group delete")
	}
	assertSingleDefaultGroup(t, ctx, pool, organizationID, group.ID)

	// Deleting a non-default group still clears memberships through the FK.
	other := createTestGroup(t, ctx, store, organizationID, suffix, "delete-non-default")
	otherUserID := insertAccessGroupTestUser(t, ctx, pool, suffix, &other.ID, 2)
	otherProfileID := insertAccessGroupTestProfile(t, ctx, pool, suffix, otherUserID, organizationID, &other.ID)
	if err := store.Delete(ctx, organizationID, other.ID); err != nil {
		t.Fatalf("Delete(non-default) error: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT access_group_id IS NOT NULL
		FROM users
		WHERE id = $1`, otherUserID).Scan(&hasGroup); err != nil {
		t.Fatalf("load deleted group member: %v", err)
	}
	if hasGroup {
		t.Fatalf("user access_group_id remained set after deleting non-default group")
	}
	var reassignedGroupID int64
	if err := pool.QueryRow(ctx, `
		SELECT access_group_id
		FROM user_profiles
		WHERE user_id = $1 AND id = $2`, otherUserID, otherProfileID).Scan(&reassignedGroupID); err != nil {
		t.Fatalf("load reassigned profile: %v", err)
	}
	if reassignedGroupID != group.ID {
		t.Fatalf("deleted-group profile reassigned to %d, want organization default %d", reassignedGroupID, group.ID)
	}
}

func newGroupStoreDBTest(t *testing.T) (context.Context, *pgxpool.Pool, *GroupStore, string, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping local PostgreSQL test")
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, accessGroupTestDatabaseConfig.Copy())
	if err != nil {
		t.Fatalf("connect disposable access-group database: %v", err)
	}
	t.Cleanup(pool.Close)

	var tableName *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.access_groups')::text`).Scan(&tableName); err != nil {
		t.Fatalf("check access_groups table: %v", err)
	}
	if tableName == nil || *tableName == "" {
		t.Skip("test database has not applied access groups migration")
	}
	if !accessGroupColumnExists(t, ctx, pool, "is_default") {
		t.Skip("test database has not applied default access group migration")
	}
	// The store reads and writes the group transcode gates on every path,
	// so a database without them cannot run any of these tests.
	for _, column := range []string{"transcode_allowed", "audio_transcode_allowed"} {
		if !accessGroupColumnExists(t, ctx, pool, column) {
			t.Skipf("test database has not applied the user policy inherit/override migration (access_groups.%s missing)", column)
		}
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	organizationID := defaultAccessGroupOrganizationID(t, ctx, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE username LIKE $1`, "access-group-test-"+suffix+"%")
		_, _ = pool.Exec(ctx, `DELETE FROM access_groups WHERE name LIKE $1`, "Access Group Test "+suffix+"%")
	})
	return ctx, pool, NewGroupStore(pool), suffix, organizationID
}

func accessGroupColumnExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, column string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'access_groups'
			  AND column_name = $1
		)`, column).Scan(&exists); err != nil {
		t.Fatalf("check access_groups.%s column: %v", column, err)
	}
	return exists
}

func prepareAccessGroupTestDatabase(ctx context.Context, dsn string) (*pgxpool.Config, func() error, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, fmt.Errorf("generate disposable database name: %w", err)
	}
	name := "vondel_access_groups_" + hex.EncodeToString(random[:])
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

func defaultAccessGroupSeedID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID uuid.UUID) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		SELECT id
		FROM access_groups
		WHERE name = 'Default Group'
		  AND is_default
		  AND organization_id = $1`, organizationID).Scan(&id); err != nil {
		t.Fatalf("load seeded default access group: %v", err)
	}
	return id
}

func assertDefaultGroupSeed(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID uuid.UUID) {
	t.Helper()
	var (
		description              string
		libraryIDsNull           bool
		maxPlaybackQuality       string
		downloadAllowed          bool
		downloadTranscodeAllowed bool
		maxStreams               int
		maxTranscodes            int
		allowedPermissions       []string
		requestsAllowed          bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT description, library_ids IS NULL, max_playback_quality,
			download_allowed, download_transcode_allowed, max_streams,
			max_transcodes, allowed_permissions, requests_allowed
		FROM access_groups
		WHERE name = 'Default Group'
		  AND is_default
		  AND organization_id = $1`, organizationID).Scan(
		&description,
		&libraryIDsNull,
		&maxPlaybackQuality,
		&downloadAllowed,
		&downloadTranscodeAllowed,
		&maxStreams,
		&maxTranscodes,
		&allowedPermissions,
		&requestsAllowed,
	); err != nil {
		t.Fatalf("load seeded default access group details: %v", err)
	}
	if description != "Applied automatically to newly created users." ||
		!libraryIDsNull ||
		maxPlaybackQuality != "" ||
		!downloadAllowed ||
		downloadTranscodeAllowed ||
		maxStreams != 5 ||
		maxTranscodes != 5 ||
		!slices.Equal(allowedPermissions, []string{"marker_edit"}) ||
		!requestsAllowed {
		t.Fatalf("seeded default group does not match the migration's starter policy")
	}
}

func assertSingleDefaultGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID uuid.UUID, wantID int64) {
	t.Helper()
	var (
		gotID int64
		count int
	)
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(MIN(id), 0), COUNT(*)::int
		FROM access_groups
		WHERE is_default
		  AND organization_id = $1`, organizationID).Scan(&gotID, &count); err != nil {
		t.Fatalf("count default access groups: %v", err)
	}
	if count != 1 || gotID != wantID {
		t.Fatalf("default groups = count %d id %d, want count 1 id %d", count, gotID, wantID)
	}
}

func restoreDefaultAccessGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID uuid.UUID, seedID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		UPDATE access_groups
		SET is_default = false
		WHERE is_default
		  AND organization_id = $1
		  AND id <> $2`, organizationID, seedID); err != nil {
		t.Fatalf("clear non-seed default groups: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE access_groups
		SET is_default = true
		WHERE organization_id = $1
		  AND id = $2`, organizationID, seedID); err != nil {
		t.Fatalf("restore seeded default group: %v", err)
	}
}

func createTestGroup(t *testing.T, ctx context.Context, store *GroupStore, organizationID uuid.UUID, suffix, label string) *Group {
	t.Helper()
	group, err := store.Create(ctx, organizationID, CreateGroupInput{
		Name:                     "Access Group Test " + suffix + " " + label,
		Description:              "test group",
		LibraryIDs:               []int{1, 3},
		MaxPlaybackQuality:       PlaybackQuality4K,
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: true,
		TranscodeAllowed:         false,
		AudioTranscodeAllowed:    true,
		MaxStreams:               3,
		MaxProfiles:              2,
		MaxTranscodes:            2,
		AllowedPermissions:       []string{"marker_edit"},
		RequestsAllowed:          true,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	return group
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

func insertAccessGroupTestUser(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	suffix string,
	groupID *int64,
	revision int64,
) int {
	t.Helper()
	username := fmt.Sprintf("access-group-test-%s-%d", suffix, time.Now().UnixNano())
	var id int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, role, enabled, access_group_id, access_policy_revision)
		VALUES ($1, 'user', true, $2, $3)
		RETURNING id`,
		username,
		groupID,
		revision,
	).Scan(&id); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	return id
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

func accessPolicyRevisionForUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int) int64 {
	t.Helper()
	var revision int64
	if err := pool.QueryRow(ctx, `
		SELECT access_policy_revision
		FROM users
		WHERE id = $1`, userID).Scan(&revision); err != nil {
		t.Fatalf("load access_policy_revision for user %d: %v", userID, err)
	}
	return revision
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
