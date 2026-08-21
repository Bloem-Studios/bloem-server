package entitlements_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

func TestEnsureExactCohortAdoptsManagedGroupWithoutPolicyDrift(t *testing.T) {
	fixture := newCohortFixture(t)
	before := fixture.snapshotGroup(fixture.managedGroupID)

	tx, err := fixture.pool.Begin(fixture.ctx)
	require.NoError(t, err)
	cohort, created, err := fixture.store.EnsureExactCohortInTx(
		fixture.ctx, tx, fixture.organizationID, "standard", 1, fixture.actorID,
	)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, tx.Commit(fixture.ctx))

	require.Equal(t, fixture.managedGroupID, cohort.AccessGroupID)
	require.Equal(t, "standard", cohort.SourceTemplateKey)
	require.Equal(t, int64(1), cohort.SourceTemplateRevision)
	require.Equal(t, "exact_template", cohort.DerivationKind)
	require.Equal(t, before, fixture.snapshotGroup(cohort.AccessGroupID))
}

func TestEnsureExactCohortAdoptsSemanticallyEquivalentNoncanonicalGroupWithoutDrift(t *testing.T) {
	fixture := newCohortFixture(t)
	_, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE access_groups
		SET library_ids=ARRAY[2,1,2]::integer[],
		    max_playback_quality='standard',
		    allowed_permissions=ARRAY['metadata_curation','marker_edit','marker_edit']::text[]
		WHERE organization_id=$1 AND id=$2`, fixture.organizationID, fixture.managedGroupID)
	require.NoError(t, err)
	before := fixture.snapshotGroup(fixture.managedGroupID)

	cohort, _ := fixture.ensureExact(t)

	require.Equal(t, before, fixture.snapshotGroup(fixture.managedGroupID))
	require.Equal(t, []int{1, 2}, cohort.Policy.LibraryIDs)
	require.Equal(t, "1080p", cohort.Policy.MaxPlaybackQuality)
	require.Equal(t, []string{"marker_edit", "metadata_curation"}, cohort.Policy.AllowedPermissions)
}

func TestEnsureExactCohortConvergesOnOneRevision(t *testing.T) {
	fixture := newCohortFixture(t)

	first, firstCreated := fixture.ensureExact(t)
	second, secondCreated := fixture.ensureExact(t)

	require.True(t, firstCreated)
	require.False(t, secondCreated)
	require.Equal(t, first, second)

	var revisions int
	require.NoError(t, fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*)::int
		FROM entitlement_policy_cohort_revisions
		WHERE organization_id=$1
		  AND source_template_key='standard'
		  AND source_template_revision=1
		  AND derivation_kind='exact_template'`, fixture.organizationID).Scan(&revisions))
	require.Equal(t, 1, revisions)
}

func TestDeriveCohortCreatesImmutableProtectedGroup(t *testing.T) {
	fixture := newCohortFixture(t)
	parent, _ := fixture.ensureExact(t)
	disabled := false

	tx, err := fixture.pool.Begin(fixture.ctx)
	require.NoError(t, err)
	derived, created, err := fixture.store.DeriveCohortInTx(
		fixture.ctx,
		tx,
		fixture.organizationID,
		parent.ID,
		"Standard without downloads",
		entitlements.PolicyPatch{
			DownloadAllowed:          &disabled,
			DownloadTranscodeAllowed: &disabled,
		},
		fixture.actorID,
	)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, tx.Commit(fixture.ctx))

	if derived.ID == parent.ID || derived.AccessGroupID == parent.AccessGroupID {
		t.Fatalf("derived cohort reused parent identity/group: parent=%+v derived=%+v", parent, derived)
	}
	require.Equal(t, parent.ID, derived.ParentID)
	require.Equal(t, "policy_patch", derived.DerivationKind)
	require.False(t, derived.Policy.DownloadAllowed)
	require.False(t, derived.Policy.DownloadTranscodeAllowed)

	mutated := "mutated"
	_, err = fixture.groupStore.Update(fixture.ctx, fixture.organizationID, derived.AccessGroupID, access.UpdateGroupInput{Name: &mutated})
	require.ErrorIs(t, err, access.ErrManagedGroup)
	err = fixture.groupStore.Delete(fixture.ctx, fixture.organizationID, derived.AccessGroupID)
	require.ErrorIs(t, err, access.ErrManagedGroup)

	_, err = fixture.pool.Exec(fixture.ctx, `
		UPDATE entitlement_policy_cohort_revisions
		SET name='mutated'
		WHERE id=$1`, derived.ID)
	if err == nil {
		t.Fatal("cohort revision update succeeded, want immutable-row rejection")
	}
	_, err = fixture.pool.Exec(fixture.ctx, `
		DELETE FROM entitlement_policy_cohort_revisions
		WHERE id=$1`, derived.ID)
	if err == nil {
		t.Fatal("cohort revision delete succeeded, want immutable-row rejection")
	}
}

func TestDeriveCohortRejectsPatchWhenUnchangedDependencyWouldBeInvalid(t *testing.T) {
	fixture := newCohortFixture(t)
	parent, _ := fixture.ensureExact(t)
	disabled := false

	tx, err := fixture.pool.Begin(fixture.ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(fixture.ctx) }()
	_, _, err = fixture.store.DeriveCohortInTx(
		fixture.ctx,
		tx,
		fixture.organizationID,
		parent.ID,
		"Invalid unchanged dependency",
		entitlements.PolicyPatch{DownloadAllowed: &disabled},
		fixture.actorID,
	)
	require.ErrorIs(t, err, entitlements.ErrInvalidPolicy)
}

func TestDeriveCohortConvergesAndLeavesParentPolicyUnchanged(t *testing.T) {
	fixture := newCohortFixture(t)
	parent, _ := fixture.ensureExact(t)
	parentBefore := fixture.snapshotGroup(parent.AccessGroupID)
	maxStreams := 2
	patch := entitlements.PolicyPatch{MaxStreams: &maxStreams}

	first, firstCreated := fixture.derive(t, parent.ID, "Standard two streams", patch)
	second, secondCreated := fixture.derive(t, parent.ID, "Standard two streams", patch)

	require.True(t, firstCreated)
	require.False(t, secondCreated)
	require.Equal(t, first, second)
	require.Equal(t, parentBefore, fixture.snapshotGroup(parent.AccessGroupID))
}

func TestDeriveCohortPreservesExplicitEmptySetsAcrossEmptyAddPatch(t *testing.T) {
	fixture := newCohortFixture(t)
	parent, _ := fixture.ensureExact(t)
	empty, _ := fixture.derive(t, parent.ID, "Standard with empty sets", entitlements.PolicyPatch{
		LibraryIDs:         &entitlements.IntegerSetPatch{Mode: entitlements.PolicyLibrariesNone},
		AllowedPermissions: &entitlements.StringSetPatch{Mode: entitlements.PolicySetReplace, Values: []string{}},
	})

	child, _ := fixture.derive(t, empty.ID, "Standard with empty sets unchanged", entitlements.PolicyPatch{
		LibraryIDs:         &entitlements.IntegerSetPatch{Mode: entitlements.PolicySetAdd, Values: []int{}},
		AllowedPermissions: &entitlements.StringSetPatch{Mode: entitlements.PolicySetAdd, Values: []string{}},
	})

	if child.Policy.LibraryIDs == nil || len(child.Policy.LibraryIDs) != 0 {
		t.Fatalf("library_ids = %#v, want explicit empty set", child.Policy.LibraryIDs)
	}
	if child.Policy.AllowedPermissions == nil || len(child.Policy.AllowedPermissions) != 0 {
		t.Fatalf("allowed_permissions = %#v, want explicit empty set", child.Policy.AllowedPermissions)
	}
}

func TestDeriveCohortPermissionPatchFromUnrestrictedUsesSetSemantics(t *testing.T) {
	fixture := newCohortFixture(t)
	parent, _ := fixture.ensureExact(t)
	if parent.Policy.AllowedPermissions != nil {
		t.Fatalf("parent permissions = %#v, want unrestricted", parent.Policy.AllowedPermissions)
	}

	added, _ := fixture.derive(t, parent.ID, "Unrestricted permissions add", entitlements.PolicyPatch{
		AllowedPermissions: &entitlements.StringSetPatch{
			Mode: entitlements.PolicySetAdd, Values: []string{"marker_edit"},
		},
	})
	if added.Policy.AllowedPermissions != nil {
		t.Fatalf("permissions after add = %#v, want unrestricted", added.Policy.AllowedPermissions)
	}

	removed, _ := fixture.derive(t, parent.ID, "Unrestricted permissions remove", entitlements.PolicyPatch{
		AllowedPermissions: &entitlements.StringSetPatch{
			Mode: entitlements.PolicySetRemove, Values: []string{"marker_edit"},
		},
	})
	require.Equal(t, []string{"metadata_curation"}, removed.Policy.AllowedPermissions)
}

func TestDeriveCohortPolicyPatchMatrix(t *testing.T) {
	fixture := newCohortFixture(t)
	firstLibraryID := insertEntitlementLibrary(t, fixture.ctx, fixture.pool, "cohort-matrix-first-"+uuid.NewString(), true)
	secondLibraryID := insertEntitlementLibrary(t, fixture.ctx, fixture.pool, "cohort-matrix-second-"+uuid.NewString(), true)
	_, err := fixture.store.ApplyTemplate(fixture.ctx, fixture.organizationID, "standard", 1, false)
	require.NoError(t, err)
	parent, _ := fixture.ensureExact(t)
	thirdLibraryID := insertEntitlementLibrary(t, fixture.ctx, fixture.pool, "cohort-matrix-third-"+uuid.NewString(), true)

	addedLibraries, _ := fixture.derive(t, parent.ID, "Matrix libraries add", entitlements.PolicyPatch{
		LibraryIDs: &entitlements.IntegerSetPatch{Mode: entitlements.PolicySetAdd, Values: []int{thirdLibraryID}},
	})
	wantAddedLibraries := append(slices.Clone(parent.Policy.LibraryIDs), thirdLibraryID)
	slices.Sort(wantAddedLibraries)
	require.Equal(t, wantAddedLibraries, addedLibraries.Policy.LibraryIDs)

	removedLibraries, _ := fixture.derive(t, parent.ID, "Matrix libraries remove", entitlements.PolicyPatch{
		LibraryIDs: &entitlements.IntegerSetPatch{Mode: entitlements.PolicySetRemove, Values: []int{firstLibraryID}},
	})
	wantRemovedLibraries := make([]int, 0, len(parent.Policy.LibraryIDs)-1)
	for _, libraryID := range parent.Policy.LibraryIDs {
		if libraryID != firstLibraryID {
			wantRemovedLibraries = append(wantRemovedLibraries, libraryID)
		}
	}
	require.Equal(t, wantRemovedLibraries, removedLibraries.Policy.LibraryIDs)

	replacedLibraries, _ := fixture.derive(t, parent.ID, "Matrix libraries replace", entitlements.PolicyPatch{
		LibraryIDs: &entitlements.IntegerSetPatch{
			Mode: entitlements.PolicySetReplace, Values: []int{thirdLibraryID, secondLibraryID, thirdLibraryID},
		},
	})
	require.Equal(t, []int{secondLibraryID, thirdLibraryID}, replacedLibraries.Policy.LibraryIDs)

	allLibraries, _ := fixture.derive(t, parent.ID, "Matrix libraries all", entitlements.PolicyPatch{
		LibraryIDs: &entitlements.IntegerSetPatch{Mode: entitlements.PolicyLibrariesAll},
	})
	require.Equal(t, entitlementEnabledLibraryIDs(t, fixture.ctx, fixture.pool), allLibraries.Policy.LibraryIDs)

	noLibraries, _ := fixture.derive(t, parent.ID, "Matrix libraries none", entitlements.PolicyPatch{
		LibraryIDs: &entitlements.IntegerSetPatch{Mode: entitlements.PolicyLibrariesNone},
	})
	if noLibraries.Policy.LibraryIDs == nil || len(noLibraries.Policy.LibraryIDs) != 0 {
		t.Fatalf("none libraries = %#v, want explicit empty set", noLibraries.Policy.LibraryIDs)
	}

	restrictedPermissions, _ := fixture.derive(t, parent.ID, "Matrix permissions replace", entitlements.PolicyPatch{
		AllowedPermissions: &entitlements.StringSetPatch{
			Mode: entitlements.PolicySetReplace, Values: []string{"marker_edit"},
		},
	})
	require.Equal(t, []string{"marker_edit"}, restrictedPermissions.Policy.AllowedPermissions)

	addedPermissions, _ := fixture.derive(t, restrictedPermissions.ID, "Matrix permissions add", entitlements.PolicyPatch{
		AllowedPermissions: &entitlements.StringSetPatch{
			Mode: entitlements.PolicySetAdd, Values: []string{"metadata_curation", "marker_edit"},
		},
	})
	require.Equal(t, []string{"marker_edit", "metadata_curation"}, addedPermissions.Policy.AllowedPermissions)

	removedPermissions, _ := fixture.derive(t, addedPermissions.ID, "Matrix permissions remove", entitlements.PolicyPatch{
		AllowedPermissions: &entitlements.StringSetPatch{
			Mode: entitlements.PolicySetRemove, Values: []string{"marker_edit"},
		},
	})
	require.Equal(t, []string{"metadata_curation"}, removedPermissions.Policy.AllowedPermissions)

	unrestrictedPermissions, _ := fixture.derive(t, restrictedPermissions.ID, "Matrix permissions unrestricted", entitlements.PolicyPatch{
		AllowedPermissions: &entitlements.StringSetPatch{Mode: entitlements.PolicySetUnrestricted},
	})
	if unrestrictedPermissions.Policy.AllowedPermissions != nil {
		t.Fatalf("unrestricted permissions = %#v, want nil", unrestrictedPermissions.Policy.AllowedPermissions)
	}

	playbackDisabled := false
	zero := 0
	playbackPolicy, _ := fixture.derive(t, parent.ID, "Matrix playback", entitlements.PolicyPatch{
		PlaybackAllowed:          &playbackDisabled,
		MaxStreams:               &zero,
		TranscodeAllowed:         &playbackDisabled,
		MaxTranscodes:            &zero,
		DownloadAllowed:          &playbackDisabled,
		DownloadTranscodeAllowed: &playbackDisabled,
	})
	require.False(t, playbackPolicy.Policy.PlaybackAllowed)
	require.False(t, playbackPolicy.Policy.TranscodeAllowed)
	require.False(t, playbackPolicy.Policy.DownloadAllowed)
	require.False(t, playbackPolicy.Policy.DownloadTranscodeAllowed)
	require.Equal(t, 0, playbackPolicy.Policy.MaxStreams)
	require.Equal(t, 0, playbackPolicy.Policy.MaxTranscodes)

	requestsDisabled := false
	requestPolicy, _ := fixture.derive(t, parent.ID, "Matrix requests", entitlements.PolicyPatch{
		RequestsAllowed: &requestsDisabled,
	})
	require.False(t, requestPolicy.Policy.RequestsAllowed)

	maxStreams, maxProfiles, maxTranscodes := 7, 9, 2
	quality := "2160p"
	limitPolicy, _ := fixture.derive(t, parent.ID, "Matrix limits and quality", entitlements.PolicyPatch{
		MaxStreams:         &maxStreams,
		MaxProfiles:        &maxProfiles,
		MaxTranscodes:      &maxTranscodes,
		MaxPlaybackQuality: &quality,
	})
	require.Equal(t, 7, limitPolicy.Policy.MaxStreams)
	require.Equal(t, 9, limitPolicy.Policy.MaxProfiles)
	require.Equal(t, 2, limitPolicy.Policy.MaxTranscodes)
	require.Equal(t, "2160p", limitPolicy.Policy.MaxPlaybackQuality)
}

func TestApplyAccountTemplateDoesNotReuseOrMutateDerivedCohortGroup(t *testing.T) {
	fixture := newCohortFixture(t)
	parent, _ := fixture.ensureExact(t)
	maxStreams := parent.Policy.MaxStreams + 1
	derived, _ := fixture.derive(t, parent.ID, "Derived account apply guard", entitlements.PolicyPatch{
		MaxStreams: &maxStreams,
	})
	before := fixture.snapshotGroup(derived.AccessGroupID)

	result, err := fixture.store.ApplyAccountTemplate(
		fixture.ctx,
		fixture.organizationID,
		fixture.actorID,
		parent.SourceTemplateKey,
		parent.SourceTemplateRevision,
		false,
	)
	require.NoError(t, err)
	if result.GroupID == derived.AccessGroupID {
		t.Fatalf("template apply reused derived cohort group %d", derived.AccessGroupID)
	}
	require.Equal(t, before, fixture.snapshotGroup(derived.AccessGroupID))
}

func TestApplyTenantTemplateRefusesToMutateCohortManagedDefault(t *testing.T) {
	fixture := newCohortFixture(t)
	cohort, _ := fixture.ensureExact(t)
	before := fixture.snapshotGroup(cohort.AccessGroupID)
	insertEntitlementLibrary(t, fixture.ctx, fixture.pool, "cohort-apply-guard-"+uuid.NewString(), true)

	_, err := fixture.store.ApplyTemplate(
		fixture.ctx,
		fixture.organizationID,
		cohort.SourceTemplateKey,
		cohort.SourceTemplateRevision,
		false,
	)
	if err == nil {
		t.Fatal("tenant template apply succeeded, want protected cohort-group rejection")
	}
	require.Equal(t, before, fixture.snapshotGroup(cohort.AccessGroupID))
}

func TestCohortLookupIsOrganizationScopedAndArchiveAware(t *testing.T) {
	fixture := newCohortFixture(t)
	cohort, _ := fixture.ensureExact(t)

	got, err := fixture.store.GetCohort(fixture.ctx, fixture.organizationID, cohort.ID)
	require.NoError(t, err)
	require.Equal(t, cohort, got)

	_, err = fixture.store.GetCohort(fixture.ctx, uuid.New(), cohort.ID)
	require.ErrorIs(t, err, entitlements.ErrCohortNotFound)

	listed, err := fixture.store.ListCohorts(fixture.ctx, fixture.organizationID, false)
	require.NoError(t, err)
	require.Equal(t, []entitlements.CohortRevision{cohort}, listed)

	_, err = fixture.pool.Exec(fixture.ctx, `
		UPDATE entitlement_policy_cohorts
		SET archived=true
		WHERE id=(SELECT cohort_id FROM entitlement_policy_cohort_revisions WHERE id=$1)`, cohort.ID)
	require.NoError(t, err)
	listed, err = fixture.store.ListCohorts(fixture.ctx, fixture.organizationID, false)
	require.NoError(t, err)
	require.Equal(t, []entitlements.CohortRevision{}, listed)
	listed, err = fixture.store.ListCohorts(fixture.ctx, fixture.organizationID, true)
	require.NoError(t, err)
	require.Equal(t, 1, len(listed))
	require.True(t, listed[0].Archived)
}

type cohortFixture struct {
	t              *testing.T
	ctx            context.Context
	pool           *pgxpool.Pool
	store          *entitlements.Store
	groupStore     *access.GroupStore
	organizationID uuid.UUID
	managedGroupID int64
	actorID        int
}

func newCohortFixture(t *testing.T) *cohortFixture {
	t.Helper()
	ctx, pool, store := entitlementTestStore(t)
	tenant, err := tenancy.NewStore(pool).CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name:               "Cohort test " + uuid.NewString(),
		ExternalOperatorID: "cohort-operator-" + uuid.NewString(),
		ExternalServiceID:  "cohort-service-" + uuid.NewString(),
		Slots:              5,
		Transcodes:         2,
	})
	require.NoError(t, err)
	applied, err := store.ApplyTemplate(ctx, tenant.ID, "standard", 1, false)
	require.NoError(t, err)

	var actorID int
	suffix := uuid.NewString()
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email,username,password_hash,role,access_group_id)
		VALUES ($1,$2,'test-hash','user',$3)
		RETURNING id`, "cohort-"+suffix+"@example.test", "cohort-"+suffix, applied.GroupID).Scan(&actorID))
	_, err = pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
		VALUES ($1,$2,'active','admin')`, tenant.ID, actorID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id,is_primary)
		VALUES ($1,$2,'Primary',$3,$4,true)`, uuid.NewString(), actorID, tenant.ID, applied.GroupID)
	require.NoError(t, err)

	return &cohortFixture{
		t: t, ctx: ctx, pool: pool, store: store, groupStore: access.NewGroupStore(pool),
		organizationID: tenant.ID, managedGroupID: applied.GroupID, actorID: actorID,
	}
}

func (f *cohortFixture) ensureExact(t *testing.T) (entitlements.CohortRevision, bool) {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	cohort, created, err := f.store.EnsureExactCohortInTx(f.ctx, tx, f.organizationID, "standard", 1, f.actorID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(f.ctx))
	return cohort, created
}

func (f *cohortFixture) derive(t *testing.T, parentID uuid.UUID, name string, patch entitlements.PolicyPatch) (entitlements.CohortRevision, bool) {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	cohort, created, err := f.store.DeriveCohortInTx(f.ctx, tx, f.organizationID, parentID, name, patch, f.actorID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(f.ctx))
	return cohort, created
}

type cohortGroupSnapshot struct {
	Name                     string
	Description              string
	IsDefault                bool
	LibraryIDs               []int
	MaxPlaybackQuality       string
	PlaybackAllowed          bool
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	TranscodeAllowed         bool
	MaxStreams               int
	MaxProfiles              int
	MaxTranscodes            int
	AllowedPermissions       []string
	RequestsAllowed          bool
	AccountIDs               []int
	ProfileIDs               []string
}

func (f *cohortFixture) snapshotGroup(groupID int64) cohortGroupSnapshot {
	f.t.Helper()
	var snapshot cohortGroupSnapshot
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `
		SELECT name,description,is_default,library_ids,max_playback_quality,
		       playback_allowed,download_allowed,download_transcode_allowed,
		       transcode_allowed,max_streams,max_profiles,max_transcodes,
		       allowed_permissions,requests_allowed
		FROM access_groups
		WHERE organization_id=$1 AND id=$2`, f.organizationID, groupID).Scan(
		&snapshot.Name, &snapshot.Description, &snapshot.IsDefault, &snapshot.LibraryIDs,
		&snapshot.MaxPlaybackQuality, &snapshot.PlaybackAllowed, &snapshot.DownloadAllowed,
		&snapshot.DownloadTranscodeAllowed, &snapshot.TranscodeAllowed, &snapshot.MaxStreams,
		&snapshot.MaxProfiles, &snapshot.MaxTranscodes, &snapshot.AllowedPermissions,
		&snapshot.RequestsAllowed,
	))
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `
		SELECT COALESCE(array_agg(id ORDER BY id) FILTER (WHERE id IS NOT NULL),'{}')
		FROM users WHERE access_group_id=$1`, groupID).Scan(&snapshot.AccountIDs))
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `
		SELECT COALESCE(array_agg(id ORDER BY id) FILTER (WHERE id IS NOT NULL),'{}')
		FROM user_profiles WHERE organization_id=$1 AND access_group_id=$2`, f.organizationID, groupID).Scan(&snapshot.ProfileIDs))
	return snapshot
}
