package entitlements_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGetAccountPolicyReturnsExactManagedProvenance(t *testing.T) {
	fixture := newCohortFixture(t)
	cohort, _ := fixture.ensureExact(t)
	var stableCohortID uuid.UUID
	require.NoError(t, fixture.pool.QueryRow(fixture.ctx,
		`SELECT cohort_id FROM entitlement_policy_cohort_revisions WHERE id=$1`, cohort.ID,
	).Scan(&stableCohortID))
	_, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET audio_transcode_allowed=true WHERE account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.actorID)
	require.NoError(t, err)

	snapshot, err := fixture.store.GetAccountPolicy(fixture.ctx, fixture.organizationID, fixture.actorID)
	require.NoError(t, err)
	require.False(t, snapshot.ObservedAt.IsZero())
	require.Equal(t, fixture.organizationID, snapshot.OrganizationID)
	require.Equal(t, fixture.actorID, snapshot.AccountID)
	require.Equal(t, cohort.AccessGroupID, snapshot.GroupID)
	require.Equal(t, stableCohortID, snapshot.CohortID)
	if snapshot.CohortID == cohort.ID {
		t.Fatalf("cohort_id = revision marker %s, want stable cohort %s", snapshot.CohortID, stableCohortID)
	}
	require.Equal(t, cohort.Revision, snapshot.CohortRevision)
	require.Equal(t, "standard", snapshot.SourceTemplateKey)
	require.Equal(t, int64(1), snapshot.SourceTemplateRevision)
	require.Equal(t, entitlements.AccountPolicyStateManaged, snapshot.State)
	require.Equal(t, cohort.Policy.LibraryIDs, snapshot.Policy.LibraryIDs)
	require.Equal(t, cohort.Policy.MaxStreams, snapshot.Policy.MaxStreams)
	require.Equal(t, []string{"marker_edit"}, snapshot.Policy.AllowedPermissions)
	require.True(t, snapshot.Policy.AudioTranscodeAllowed)
	require.Equal(t, int64(1), snapshot.PolicyRevision)
	if len(snapshot.Profiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(snapshot.Profiles))
	}
	require.True(t, snapshot.Profiles[0].InheritsAccount)
	require.Equal(t, entitlements.AccountPolicyStateManaged, snapshot.Profiles[0].State)
	require.Equal(t, snapshot.Policy, snapshot.Profiles[0].Policy)
}

func TestGetAccountPolicyReturnsDerivedCohortAndProfileExceptions(t *testing.T) {
	fixture := newCohortFixture(t)
	parent, _ := fixture.ensureExact(t)
	maxStreams := 1
	derived, _ := fixture.derive(t, parent.ID, "Selection-specific", entitlements.PolicyPatch{MaxStreams: &maxStreams})
	var stableDerivedCohortID uuid.UUID
	require.NoError(t, fixture.pool.QueryRow(fixture.ctx,
		`SELECT cohort_id FROM entitlement_policy_cohort_revisions WHERE id=$1`, derived.ID,
	).Scan(&stableDerivedCohortID))

	var inheritedProfileID string
	require.NoError(t, fixture.pool.QueryRow(fixture.ctx, `
		SELECT id FROM user_profiles
		WHERE organization_id=$1 AND user_id=$2 AND is_primary`, fixture.organizationID, fixture.actorID).Scan(&inheritedProfileID))
	_, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships m SET access_group_id=$2 FROM access_groups g WHERE g.id=$2 AND m.organization_id=g.organization_id AND m.account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.actorID, derived.AccessGroupID)
	require.NoError(t, err)
	_, err = fixture.pool.Exec(fixture.ctx, `
		UPDATE user_profiles SET access_group_id=$2
		WHERE organization_id=$3 AND user_id=$1 AND id=$4`,
		fixture.actorID, derived.AccessGroupID, fixture.organizationID, inheritedProfileID)
	require.NoError(t, err)

	customGroupID := insertAccountPolicyGroup(t, fixture, "Custom profile", []int{}, 7)
	customProfileID := uuid.NewString()
	_, err = fixture.pool.Exec(fixture.ctx, `
		INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id)
		VALUES ($1,$2,'Custom exception',$3,$4)`, customProfileID, fixture.actorID, fixture.organizationID, customGroupID)
	require.NoError(t, err)

	snapshot, err := fixture.store.GetAccountPolicy(fixture.ctx, fixture.organizationID, fixture.actorID)
	require.NoError(t, err)
	require.Equal(t, stableDerivedCohortID, snapshot.CohortID)
	require.Equal(t, parent.SourceTemplateKey, snapshot.SourceTemplateKey)
	require.Equal(t, parent.SourceTemplateRevision, snapshot.SourceTemplateRevision)
	require.Equal(t, maxStreams, snapshot.Policy.MaxStreams)
	if len(snapshot.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(snapshot.Profiles))
	}

	profiles := map[string]entitlements.ProfilePolicySnapshot{}
	for _, profile := range snapshot.Profiles {
		profiles[profile.ProfileID] = profile
	}
	require.True(t, profiles[inheritedProfileID].InheritsAccount)
	require.Equal(t, derived.AccessGroupID, profiles[inheritedProfileID].GroupID)
	require.Equal(t, entitlements.AccountPolicyStateManaged, profiles[inheritedProfileID].State)
	require.Equal(t, maxStreams, profiles[inheritedProfileID].Policy.MaxStreams)
	require.False(t, profiles[customProfileID].InheritsAccount)
	require.Equal(t, customGroupID, profiles[customProfileID].GroupID)
	require.Equal(t, entitlements.AccountPolicyStateCustom, profiles[customProfileID].State)
	require.Equal(t, 7, profiles[customProfileID].Policy.MaxStreams)
}

func TestGetAccountPolicyResolvesDynamicAllLibrariesForCustomGroup(t *testing.T) {
	fixture := newCohortFixture(t)
	insertEntitlementLibrary(t, fixture.ctx, fixture.pool, "policy-read-"+uuid.NewString(), true)
	wantLibraries := entitlementEnabledLibraryIDs(t, fixture.ctx, fixture.pool)
	customGroupID := insertAccountPolicyGroup(t, fixture, "Dynamic all", nil, 4)
	_, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships m SET access_group_id=$2 FROM access_groups g WHERE g.id=$2 AND m.organization_id=g.organization_id AND m.account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.actorID, customGroupID)
	require.NoError(t, err)

	snapshot, err := fixture.store.GetAccountPolicy(fixture.ctx, fixture.organizationID, fixture.actorID)
	require.NoError(t, err)
	require.Equal(t, entitlements.AccountPolicyStateCustom, snapshot.State)
	require.Equal(t, customGroupID, snapshot.GroupID)
	require.True(t, slices.Equal(wantLibraries, snapshot.Policy.LibraryIDs), "libraries = %v, want %v", snapshot.Policy.LibraryIDs, wantLibraries)
	require.Equal(t, 4, snapshot.Policy.MaxStreams)
	require.Equal(t, uuid.Nil, snapshot.CohortID)
	require.Equal(t, "", snapshot.SourceTemplateKey)
}

func TestGetAccountPolicyDoesNotSynthesizeMissingCohortProvenance(t *testing.T) {
	fixture := newCohortFixture(t)
	_, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET access_group_id=NULL WHERE account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.actorID)
	require.NoError(t, err)

	snapshot, err := fixture.store.GetAccountPolicy(fixture.ctx, fixture.organizationID, fixture.actorID)
	require.NoError(t, err)
	require.Equal(t, entitlements.AccountPolicyStateLegacyUnmanaged, snapshot.State)
	require.Equal(t, int64(0), snapshot.GroupID)
	require.Equal(t, uuid.Nil, snapshot.CohortID)
	require.Equal(t, int64(0), snapshot.CohortRevision)
	require.Equal(t, "", snapshot.SourceTemplateKey)
	require.Equal(t, int64(0), snapshot.SourceTemplateRevision)
}

func TestGetAccountPolicyRejectsCrossOrganizationAccount(t *testing.T) {
	fixture := newCohortFixture(t)
	_, err := fixture.store.GetAccountPolicy(fixture.ctx, uuid.New(), fixture.actorID)
	require.ErrorIs(t, err, entitlements.ErrAccountNotFound)
}

func TestEntitlementSnapshotUsesOneObservationAndSafeNotFoundResults(t *testing.T) {
	fixture := newCohortFixture(t)
	fixture.ensureExact(t)
	missingID := fixture.actorID + 1000000
	wantSnapshot, err := fixture.store.GetAccountPolicy(fixture.ctx, fixture.organizationID, fixture.actorID)
	require.NoError(t, err)

	items, observedAt, err := fixture.store.GetAccountPolicies(fixture.ctx, fixture.organizationID, []int{fixture.actorID, missingID})
	require.NoError(t, err)
	require.False(t, observedAt.IsZero())
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Snapshot == nil {
		t.Fatal("first snapshot is nil")
	}
	require.Equal(t, observedAt, items[0].Snapshot.ObservedAt)
	wantSnapshot.ObservedAt = observedAt
	require.Equal(t, wantSnapshot, *items[0].Snapshot)
	require.Equal(t, fixture.actorID, items[0].AccountID)
	require.Nil(t, items[1].Snapshot)
	require.Equal(t, missingID, items[1].AccountID)
	require.Equal(t, entitlements.AccountPolicyResultNotFound, items[1].Error)
}

func TestEntitlementSnapshotDirectScopeUsesDefaultOrganization(t *testing.T) {
	fixture := newCohortFixture(t)
	var organizationID uuid.UUID
	var groupID int64
	require.NoError(t, fixture.pool.QueryRow(fixture.ctx, `
		SELECT organizations.id,groups.id
		FROM organizations
		JOIN access_groups groups ON groups.organization_id=organizations.id AND groups.is_default
		WHERE organizations.is_default`).Scan(&organizationID, &groupID))

	suffix := uuid.NewString()
	var accountID int
	require.NoError(t, fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO users (email,username,password_hash,role)
		VALUES ($1,$2,'test-hash','user') RETURNING id`,
		"policy-direct-"+suffix+"@example.test", "policy-direct-"+suffix).Scan(&accountID))
	_, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role,access_group_id)
		SELECT $1,$2,'active','user',$3
		WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, organizationID, accountID, groupID)
	require.NoError(t, err)
	_, err = fixture.pool.Exec(fixture.ctx, `
		INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id,is_primary)
		VALUES ($1,$2,'Primary',$3,$4,true)`, uuid.NewString(), accountID, organizationID, groupID)
	require.NoError(t, err)

	want, err := fixture.store.GetAccountPolicy(fixture.ctx, organizationID, accountID)
	require.NoError(t, err)
	items, observedAt, err := fixture.store.GetAccountPolicies(fixture.ctx, uuid.Nil, []int{accountID})
	require.NoError(t, err)
	if len(items) != 1 || items[0].Snapshot == nil {
		t.Fatalf("items = %+v, want one direct default-organization snapshot", items)
	}
	want.ObservedAt = observedAt
	require.Equal(t, want, *items[0].Snapshot)
}

func TestEntitlementSnapshotRejectsMoreThanTenThousandIDs(t *testing.T) {
	fixture := newCohortFixture(t)
	accountIDs := make([]int, entitlements.MaxAccountPolicySnapshotIDs+1)
	for index := range accountIDs {
		accountIDs[index] = index + 1
	}

	started := time.Now()
	_, _, err := fixture.store.GetAccountPolicies(fixture.ctx, fixture.organizationID, accountIDs)
	require.True(t, errors.Is(err, entitlements.ErrAccountPolicySnapshotLimit), "error = %v", err)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("limit rejection took %v, want under 1s", elapsed)
	}
}

// TestEntitlementSnapshotMaxBatchUsesFixedQueryBudget rejects an N+1 bulk
// implementation. The maximum accepted request must cost a fixed number of
// statements even when every item resolves an account and profile policy.
func TestEntitlementSnapshotMaxBatchUsesFixedQueryBudget(t *testing.T) {
	fixture := newCohortFixture(t)
	fixture.ensureExact(t)
	config, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	require.NoError(t, err)
	tracer := &accountPolicyQueryTracer{}
	config.ConnConfig.Tracer = tracer
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(fixture.ctx, config)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	accountIDs := make([]int, entitlements.MaxAccountPolicySnapshotIDs)
	for index := range accountIDs {
		accountIDs[index] = fixture.actorID
	}
	tracer.reset()
	items, observedAt, err := entitlements.NewTemplateStore(pool).GetAccountPolicies(
		fixture.ctx, fixture.organizationID, accountIDs,
	)
	require.NoError(t, err)
	require.False(t, observedAt.IsZero())
	if len(items) != entitlements.MaxAccountPolicySnapshotIDs {
		t.Fatalf("items = %d, want %d", len(items), entitlements.MaxAccountPolicySnapshotIDs)
	}
	for index, item := range items {
		if item.AccountID != accountIDs[index] || item.Error != "" || item.Snapshot == nil || len(item.Snapshot.Profiles) != 1 {
			t.Fatalf("item %d = %+v, want account and profile snapshot for %d", index, item, accountIDs[index])
		}
	}
	if queries := tracer.snapshot(); len(queries) > 6 {
		t.Fatalf("maximum bulk snapshot issued %d queries, want at most 6 fixed queries", len(queries))
	}
}

type accountPolicyQueryTracer struct {
	mu      sync.Mutex
	queries []string
}

func (c *accountPolicyQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, data.SQL)
	return ctx
}

func (*accountPolicyQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *accountPolicyQueryTracer) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = nil
}

func (c *accountPolicyQueryTracer) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.queries...)
}

func insertAccountPolicyGroup(t *testing.T, fixture *cohortFixture, name string, libraryIDs []int, maxStreams int) int64 {
	t.Helper()
	var groupID int64
	require.NoError(t, fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO access_groups (
			organization_id,name,description,is_default,library_ids,max_playback_quality,
			playback_allowed,download_allowed,download_transcode_allowed,transcode_allowed,
			audio_transcode_allowed,max_streams,max_profiles,max_transcodes,requests_allowed
		) VALUES ($1,$2,'Account policy test',false,$3,'1080p',true,true,false,true,true,$4,5,2,true)
		RETURNING id`, fixture.organizationID, name+" "+uuid.NewString(), libraryIDs, maxStreams).Scan(&groupID))
	return groupID
}
