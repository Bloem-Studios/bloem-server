package access

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
		FROM organization_memberships
		WHERE access_group_id = $1`, group.ID,
	).Scan(&assigned); err != nil {
		t.Fatalf("count memberships still on the deleted group: %v", err)
	}
	if assigned != 0 {
		t.Fatalf("memberships still on the deleted group = %d, want 0", assigned)
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
		FROM organization_memberships
		WHERE account_id = $1`, userID).Scan(&hasGroup); err != nil {
		t.Fatalf("load default group member: %v", err)
	}
	if !hasGroup {
		t.Fatalf("membership access_group_id cleared by rejected default-group delete")
	}
	assertSingleDefaultGroup(t, ctx, pool, organizationID, group.ID)

	// Deleting a non-default group still clears memberships through the FK.
	other := createTestGroup(t, ctx, store, organizationID, suffix, "delete-non-default")
	otherUserID := insertAccessGroupTestUser(t, ctx, pool, suffix, &other.ID, 2)
	otherProfileID := insertAccessGroupTestProfile(t, ctx, pool, suffix, otherUserID, organizationID, &other.ID)
	if err := store.Delete(ctx, organizationID, other.ID); err != nil {
		t.Fatalf("Delete(non-default) error: %v", err)
	}
	// Bloem reassigns a deleted group's members to the organization default
	// rather than detaching them the way upstream Silo's ON DELETE SET NULL
	// does, so the membership must now point at the default group.
	var reassignedMembershipGroupID int64
	if err := pool.QueryRow(ctx, `
		SELECT access_group_id
		FROM organization_memberships
		WHERE account_id = $1`, otherUserID).Scan(&reassignedMembershipGroupID); err != nil {
		t.Fatalf("load deleted group member: %v", err)
	}
	if reassignedMembershipGroupID != group.ID {
		t.Fatalf("membership group after delete = %d, want the default %d", reassignedMembershipGroupID, group.ID)
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
		INSERT INTO users (username, role, enabled)
		VALUES ($1, 'user', true)
		RETURNING id`, username).Scan(&id); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	// The account's group and policy revision live on its membership now, not on
	// users: finalization renames those columns away. Derive the organization
	// from the group so no call site has to thread one through.
	var organizationID uuid.UUID
	if groupID != nil {
		if err := pool.QueryRow(ctx,
			`SELECT organization_id FROM access_groups WHERE id = $1`, *groupID).Scan(&organizationID); err != nil {
			t.Fatalf("resolve organization for group %d: %v", *groupID, err)
		}
	} else if err := pool.QueryRow(ctx,
		`SELECT id FROM organizations WHERE is_default`).Scan(&organizationID); err != nil {
		t.Fatalf("resolve default organization: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin membership seed: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.MarkMembershipPolicyWriter(ctx, tx); err != nil {
		t.Fatalf("mark membership policy writer: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_memberships
			(organization_id, account_id, status, legacy_role, access_group_id, access_policy_revision)
		VALUES ($1, $2, 'active', 'user', $3, $4)
		ON CONFLICT (organization_id, account_id) DO UPDATE
		SET access_group_id = EXCLUDED.access_group_id,
			access_policy_revision = EXCLUDED.access_policy_revision`,
		organizationID, id, groupID, revision); err != nil {
		t.Fatalf("seed account membership: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit membership seed: %v", err)
	}
	return id
}

func accessPolicyRevisionForUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int) int64 {
	t.Helper()
	var revision int64
	if err := pool.QueryRow(ctx, `
		SELECT access_policy_revision
		FROM organization_memberships
		WHERE account_id = $1`, userID).Scan(&revision); err != nil {
		t.Fatalf("load access_policy_revision for account %d: %v", userID, err)
	}
	return revision
}
