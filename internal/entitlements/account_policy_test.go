package entitlements_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/google/uuid"
)

func TestGetAccountPolicyReturnsExactManagedProvenance(t *testing.T) {
	fixture := newCohortFixture(t)
	cohort, _ := fixture.ensureExact(t)

	snapshot, err := fixture.store.GetAccountPolicy(fixture.ctx, fixture.organizationID, fixture.actorID)
	require.NoError(t, err)
	require.False(t, snapshot.ObservedAt.IsZero())
	require.Equal(t, fixture.organizationID, snapshot.OrganizationID)
	require.Equal(t, fixture.actorID, snapshot.AccountID)
	require.Equal(t, cohort.AccessGroupID, snapshot.GroupID)
	require.Equal(t, cohort.ID, snapshot.CohortID)
	require.Equal(t, cohort.Revision, snapshot.CohortRevision)
	require.Equal(t, "standard", snapshot.SourceTemplateKey)
	require.Equal(t, int64(1), snapshot.SourceTemplateRevision)
	require.Equal(t, entitlements.AccountPolicyStateManaged, snapshot.State)
	require.Equal(t, cohort.Policy.LibraryIDs, snapshot.Policy.LibraryIDs)
	require.Equal(t, cohort.Policy.MaxStreams, snapshot.Policy.MaxStreams)
	require.Equal(t, []string{"marker_edit"}, snapshot.Policy.AllowedPermissions)
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

	var inheritedProfileID string
	require.NoError(t, fixture.pool.QueryRow(fixture.ctx, `
		SELECT id FROM user_profiles
		WHERE organization_id=$1 AND user_id=$2 AND is_primary`, fixture.organizationID, fixture.actorID).Scan(&inheritedProfileID))
	_, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET access_group_id=$2 WHERE id=$1`, fixture.actorID, derived.AccessGroupID)
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
	require.Equal(t, derived.ID, snapshot.CohortID)
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
	_, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET access_group_id=$2 WHERE id=$1`, fixture.actorID, customGroupID)
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
	_, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET access_group_id=NULL WHERE id=$1`, fixture.actorID)
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
	require.Equal(t, fixture.actorID, items[0].AccountID)
	require.Nil(t, items[1].Snapshot)
	require.Equal(t, missingID, items[1].AccountID)
	require.Equal(t, entitlements.AccountPolicyResultNotFound, items[1].Error)
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
