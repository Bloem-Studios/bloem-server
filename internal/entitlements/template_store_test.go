package entitlements_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

type testRequirements struct{}

var require testRequirements

func (testRequirements) NoError(t *testing.T, err error, message ...any) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v %v", err, message)
	}
}

func (testRequirements) ErrorIs(t *testing.T, err, target error, message ...any) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(%v) %v", err, target, message)
	}
}

func (testRequirements) Equal(t *testing.T, want, got any, message ...any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("got = %#v, want %#v %v", got, want, message)
	}
}

func (testRequirements) True(t *testing.T, value bool, message ...any) {
	t.Helper()
	if !value {
		t.Fatalf("got false, want true %v", message)
	}
}

func (testRequirements) False(t *testing.T, value bool, message ...any) {
	t.Helper()
	if value {
		t.Fatalf("got true, want false %v", message)
	}
}

func (testRequirements) Nil(t *testing.T, value any, message ...any) {
	t.Helper()
	v := reflect.ValueOf(value)
	if value != nil && (!v.IsValid() || (v.Kind() != reflect.Ptr && v.Kind() != reflect.Slice && v.Kind() != reflect.Map && v.Kind() != reflect.Interface && v.Kind() != reflect.Func) || !v.IsNil()) {
		t.Fatalf("got %#v, want nil %v", value, message)
	}
}

func (testRequirements) ElementsMatch(t *testing.T, want, got []int, message ...any) {
	t.Helper()
	want = slices.Clone(want)
	got = slices.Clone(got)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Fatalf("got elements %v, want %v %v", got, want, message)
	}
}

func (testRequirements) NotContains(t *testing.T, values []int, value int, message ...any) {
	t.Helper()
	if slices.Contains(values, value) {
		t.Fatalf("%v unexpectedly contains %d %v", values, value, message)
	}
}

func TestPolicyRejectsTranscodedDownloadsWithoutDownloads(t *testing.T) {
	policy := standardPolicy(nil)
	policy.DownloadAllowed = false

	require.ErrorIs(t, entitlements.ValidatePolicy(policy), entitlements.ErrInvalidPolicy)
}

func TestTemplateRevisionIsImmutable(t *testing.T) {
	ctx, _, store := entitlementTestStore(t)
	key := entitlementTestKey(t, "standard")

	first, err := store.Create(ctx, entitlements.CreateTemplateInput{
		Key: key, Name: "Standard " + key, Enabled: true, Policy: standardPolicy([]int{11, 12}),
	})
	require.NoError(t, err)

	secondPolicy := premiumPolicy([]int{11, 12})
	second, err := store.Revise(ctx, key, first.Revision, entitlements.ReviseTemplateInput{
		Name: first.Name, Enabled: true, Policy: secondPolicy,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Revision)
	require.Equal(t, int64(2), second.Revision)

	storedFirst, err := store.Get(ctx, key, 1)
	require.NoError(t, err)
	require.Equal(t, 3, storedFirst.Policy.MaxStreams)
	require.Equal(t, 1, storedFirst.Policy.MaxTranscodes)
	require.Equal(t, []string{"marker_edit"}, storedFirst.Policy.AllowedPermissions)
	require.Equal(t, 4, second.Policy.MaxStreams)
	require.Equal(t, 2, second.Policy.MaxTranscodes)

	_, err = store.Revise(ctx, key, first.Revision, entitlements.ReviseTemplateInput{
		Name: first.Name, Enabled: true, Policy: standardPolicy(nil),
	})
	require.ErrorIs(t, err, entitlements.ErrRevisionConflict)
}

func TestTemplateCloneHasIndependentHistory(t *testing.T) {
	ctx, _, store := entitlementTestStore(t)
	sourceKey := entitlementTestKey(t, "source")
	cloneKey := entitlementTestKey(t, "clone")

	source, err := store.Create(ctx, entitlements.CreateTemplateInput{
		Key: sourceKey, Name: "Source " + sourceKey, Enabled: true, Policy: standardPolicy([]int{21}),
	})
	require.NoError(t, err)
	clone, err := store.Clone(ctx, source.Key, source.Revision, entitlements.CreateTemplateInput{
		Key: cloneKey, Name: "Clone " + cloneKey, Enabled: true,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), clone.Revision)
	require.Equal(t, source.Policy, clone.Policy)

	_, err = store.Revise(ctx, source.Key, source.Revision, entitlements.ReviseTemplateInput{
		Name: source.Name, Enabled: true, Policy: premiumPolicy([]int{21}),
	})
	require.NoError(t, err)
	storedClone, err := store.Latest(ctx, clone.Key)
	require.NoError(t, err)
	require.Equal(t, int64(1), storedClone.Revision)
	require.Equal(t, 3, storedClone.Policy.MaxStreams)
}

func TestApplyTemplateMaterializesAllEnabledLibrariesAndPreservesCustomGroups(t *testing.T) {
	ctx, pool, store := entitlementTestStore(t)
	tenantStore := tenancy.NewStore(pool)
	suffix := entitlementTestKey(t, "apply")

	firstLibraryID := insertEntitlementLibrary(t, ctx, pool, "first-"+suffix, true)
	secondLibraryID := insertEntitlementLibrary(t, ctx, pool, "second-"+suffix, true)
	disabledLibraryID := insertEntitlementLibrary(t, ctx, pool, "disabled-"+suffix, false)
	enabledLibraryIDs := entitlementEnabledLibraryIDs(t, ctx, pool)
	tenant, err := tenantStore.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Entitlement tenant", ExternalOperatorID: "operator-" + suffix,
		ExternalServiceID: "service-" + suffix, Slots: 3, Transcodes: 2,
	})
	require.NoError(t, err)

	var customGroupID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO access_groups (organization_id, name, description, is_default, max_streams)
		VALUES ($1, $2, 'custom policy', false, 99)
		RETURNING id`, tenant.ID, "Custom "+suffix).Scan(&customGroupID)
	require.NoError(t, err)

	template, err := store.Create(ctx, entitlements.CreateTemplateInput{
		Key: suffix, Name: "Dynamic libraries " + suffix, Enabled: true, Policy: standardPolicy(nil),
	})
	require.NoError(t, err)
	require.Nil(t, template.Policy.LibraryIDs, "nil is the durable dynamic-all selection")

	dryRun, err := store.ApplyTemplate(ctx, tenant.ID, template.Key, template.Revision, true)
	require.NoError(t, err)
	require.True(t, dryRun.DryRun)
	require.True(t, dryRun.Changed)
	require.ElementsMatch(t, enabledLibraryIDs, dryRun.Policy.LibraryIDs)
	require.True(t, slices.Contains(dryRun.Policy.LibraryIDs, firstLibraryID))
	require.True(t, slices.Contains(dryRun.Policy.LibraryIDs, secondLibraryID))
	require.NotContains(t, dryRun.Policy.LibraryIDs, disabledLibraryID)

	var appliedKey *string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT managed_template_key FROM access_groups
		WHERE organization_id=$1 AND is_default`, tenant.ID).Scan(&appliedKey))
	require.Nil(t, appliedKey, "dry-run must not mutate the managed group")

	applied, err := store.ApplyTemplate(ctx, tenant.ID, template.Key, template.Revision, false)
	require.NoError(t, err)
	require.False(t, applied.DryRun)
	require.True(t, applied.Changed)
	require.ElementsMatch(t, enabledLibraryIDs, applied.Policy.LibraryIDs)

	var (
		groupKey      string
		groupRevision int64
		libraryIDs    []int
		maxStreams    int
	)
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT managed_template_key, managed_template_revision, library_ids, max_streams
		FROM access_groups WHERE organization_id=$1 AND is_default`, tenant.ID).
		Scan(&groupKey, &groupRevision, &libraryIDs, &maxStreams))
	require.Equal(t, template.Key, groupKey)
	require.Equal(t, template.Revision, groupRevision)
	require.ElementsMatch(t, enabledLibraryIDs, libraryIDs)
	require.Equal(t, 3, maxStreams)

	var customStreams int
	require.NoError(t, pool.QueryRow(ctx, `SELECT max_streams FROM access_groups WHERE id=$1`, customGroupID).Scan(&customStreams))
	require.Equal(t, 99, customStreams, "template apply must not mutate custom groups")

	idempotent, err := store.ApplyTemplate(ctx, tenant.ID, template.Key, template.Revision, false)
	require.NoError(t, err)
	require.False(t, idempotent.Changed)
}

func TestTenantCreationPinsTemplateInCreationTransaction(t *testing.T) {
	ctx, pool, store := entitlementTestStore(t)
	tenantStore := tenancy.NewStore(pool)
	key := entitlementTestKey(t, "create")
	template, err := store.Create(ctx, entitlements.CreateTemplateInput{
		Key: key, Name: "Provisioned " + key, Enabled: true, Policy: standardPolicy([]int{}),
	})
	require.NoError(t, err)

	tenant, err := tenantStore.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Pinned tenant", ExternalOperatorID: "operator-" + key,
		ExternalServiceID: "service-" + key, Slots: 3, Transcodes: 2,
		EntitlementTemplateKey: template.Key, EntitlementTemplateRevision: template.Revision,
	})
	require.NoError(t, err)

	var groupRevision int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT managed_template_revision FROM access_groups
		WHERE organization_id=$1 AND is_default`, tenant.ID).Scan(&groupRevision))
	require.Equal(t, template.Revision, groupRevision)

	_, err = tenantStore.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Rejected tenant", ExternalOperatorID: "operator-rejected-" + key,
		ExternalServiceID: "service-rejected-" + key, Slots: 3, Transcodes: 2,
		EntitlementTemplateKey: "missing-template", EntitlementTemplateRevision: 1,
	})
	require.ErrorIs(t, err, entitlements.ErrTemplateNotFound)
	var created bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM organizations WHERE external_service_id=$1)`, "service-rejected-"+key).Scan(&created))
	require.False(t, created, "template rejection must roll back tenant creation")
}

func TestConcurrentFirstApplyIsIdempotent(t *testing.T) {
	ctx, pool, store := entitlementTestStore(t)
	tenantStore := tenancy.NewStore(pool)
	key := entitlementTestKey(t, "concurrent")
	template, err := store.Create(ctx, entitlements.CreateTemplateInput{
		Key: key, Name: "Concurrent " + key, Enabled: true, Policy: standardPolicy([]int{}),
	})
	require.NoError(t, err)

	for attempt := 0; attempt < 8; attempt++ {
		tenant, err := tenantStore.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
			Name: "Concurrent tenant", ExternalOperatorID: fmt.Sprintf("operator-%s-%d", key, attempt),
			ExternalServiceID: fmt.Sprintf("service-%s-%d", key, attempt), Slots: 3, Transcodes: 2,
		})
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `
			UPDATE access_groups SET name=$2, description='administrator-owned default'
			WHERE organization_id=$1 AND is_default`, tenant.ID, fmt.Sprintf("Custom default %d", attempt))
		require.NoError(t, err)

		start := make(chan struct{})
		type outcome struct {
			result entitlements.ApplyResult
			err    error
		}
		outcomes := make(chan outcome, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for range 2 {
			go func() {
				ready.Done()
				<-start
				result, err := store.ApplyTemplate(ctx, tenant.ID, template.Key, template.Revision, false)
				outcomes <- outcome{result: result, err: err}
			}()
		}
		ready.Wait()
		close(start)
		first := <-outcomes
		second := <-outcomes
		require.NoError(t, first.err)
		require.NoError(t, second.err)
		require.Equal(t, first.result.GroupID, second.result.GroupID)
	}
}

func TestDisabledAndArchivedTemplatesCannotBeApplied(t *testing.T) {
	ctx, pool, store := entitlementTestStore(t)
	tenantStore := tenancy.NewStore(pool)
	key := entitlementTestKey(t, "disabled")
	template, err := store.Create(ctx, entitlements.CreateTemplateInput{
		Key: key, Name: "Disabled " + key, Enabled: false, Policy: standardPolicy(nil),
	})
	require.NoError(t, err)
	tenant, err := tenantStore.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Safe tenant", ExternalOperatorID: "operator-" + key,
		ExternalServiceID: "service-" + key, Slots: 3, Transcodes: 2,
	})
	require.NoError(t, err)

	_, err = store.ApplyTemplate(ctx, tenant.ID, template.Key, template.Revision, false)
	require.ErrorIs(t, err, entitlements.ErrTemplateUnavailable)

	enabled, err := store.Revise(ctx, template.Key, template.Revision, entitlements.ReviseTemplateInput{
		Name: template.Name, Enabled: true, Policy: template.Policy,
	})
	require.NoError(t, err)
	archived, err := store.Archive(ctx, enabled.Key, enabled.Revision)
	require.NoError(t, err)
	require.True(t, archived.Archived)
	_, err = store.ApplyTemplate(ctx, tenant.ID, archived.Key, archived.Revision, false)
	require.ErrorIs(t, err, entitlements.ErrTemplateUnavailable)

	_, err = store.Get(ctx, "does-not-exist", 1)
	require.True(t, errors.Is(err, entitlements.ErrTemplateNotFound))
}

func TestManagedTemplateGroupRejectsGenericMutationAndDeletion(t *testing.T) {
	ctx, pool, store := entitlementTestStore(t)
	tenantStore := tenancy.NewStore(pool)
	key := entitlementTestKey(t, "protected")
	template, err := store.Create(ctx, entitlements.CreateTemplateInput{
		Key: key, Name: "Protected " + key, Enabled: true, Policy: standardPolicy([]int{}),
	})
	require.NoError(t, err)
	tenant, err := tenantStore.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Protected tenant", ExternalOperatorID: "operator-" + key,
		ExternalServiceID: "service-" + key, Slots: 3, Transcodes: 2,
		EntitlementTemplateKey: template.Key, EntitlementTemplateRevision: template.Revision,
	})
	require.NoError(t, err)
	var groupID int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT id FROM access_groups
		WHERE organization_id=$1 AND managed_template_key=$2`, tenant.ID, template.Key).Scan(&groupID))

	groupStore := access.NewGroupStore(pool)
	maxStreams := 44
	_, err = groupStore.Update(ctx, tenant.ID, groupID, access.UpdateGroupInput{MaxStreams: &maxStreams})
	require.ErrorIs(t, err, access.ErrManagedGroup)
	err = groupStore.Delete(ctx, tenant.ID, groupID)
	require.ErrorIs(t, err, access.ErrManagedGroup)
}

func standardPolicy(libraryIDs []int) entitlements.Policy {
	return entitlements.Policy{
		LibraryIDs: libraryIDs, PlaybackAllowed: true, MaxStreams: 3, MaxProfiles: 5,
		TranscodeAllowed: true, MaxTranscodes: 1, DownloadAllowed: true,
		DownloadTranscodeAllowed: true, MaxPlaybackQuality: "1080p", RequestsAllowed: true,
		AllowedPermissions: []string{"marker_edit"},
	}
}

func premiumPolicy(libraryIDs []int) entitlements.Policy {
	policy := standardPolicy(libraryIDs)
	policy.MaxStreams = 4
	policy.MaxTranscodes = 2
	policy.MaxPlaybackQuality = "2160p"
	return policy
}

func entitlementTestStore(t *testing.T) (context.Context, *pgxpool.Pool, *entitlements.Store) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping PostgreSQL entitlement test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, database.RunMigrations(ctx, pool, migrations.FS, "sql"))
	return ctx, pool, entitlements.NewTemplateStore(pool)
}

func entitlementTestKey(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%s-%d", prefix, uuid.NewString()[:8], time.Now().UnixNano())
}

func insertEntitlementLibrary(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, enabled bool) int {
	t.Helper()
	var id int
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO media_folders (type, name, enabled)
		VALUES ('movies', $1, $2) RETURNING id`, name, enabled).Scan(&id))
	return id
}

func entitlementEnabledLibraryIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []int {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT id FROM media_folders WHERE enabled ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}
