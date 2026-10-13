package entitlements_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/entitlements"
)

func requireImmutableCohortRevision(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "entitlement policy cohort revisions are immutable") {
		t.Fatalf("err = %v, want the cohort revision immutability refusal", err)
	}
}

// The first direct-account apply of a template revision creates its cohort.
// The administrator who made the change is the creator, not the account being
// provisioned, so that account can still be deleted afterwards.
func TestDirectAccountApplyRecordsActingAdministratorAsCohortCreator(t *testing.T) {
	ctx, pool, store := entitlementTestStore(t)
	var organizationID uuid.UUID
	var defaultGroupID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT o.id,g.id FROM organizations o JOIN access_groups g ON g.organization_id=o.id AND g.is_default WHERE o.is_default`).Scan(&organizationID, &defaultGroupID))
	template, err := store.Create(ctx, entitlements.CreateTemplateInput{Key: entitlementTestKey(t, "cohort-creator"), Name: "Cohort creator " + uuid.NewString(), Enabled: true, Policy: standardPolicy([]int{})})
	require.NoError(t, err)
	actorID := insertEntitlementActor(t, ctx, pool)
	accountID := insertDirectEntitlementAccount(t, ctx, pool, organizationID, defaultGroupID, "cohort-creator-target")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = store.ApplyDefaultAccountTemplateInTransaction(ctx, tx, actorID, accountID, template.Key, template.Revision, false)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var revisionID uuid.UUID
	var creatorID int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT id,created_by_account_id FROM entitlement_policy_cohort_revisions
		WHERE organization_id=$1 AND source_template_key=$2 AND source_template_revision=$3`,
		organizationID, template.Key, template.Revision).Scan(&revisionID, &creatorID))
	require.Equal(t, actorID, creatorID)

	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, accountID)
	require.NoError(t, err)
}

// Deleting a cohort revision's creator clears the creator and keeps the
// revision. Revisions remain otherwise immutable.
func TestCohortRevisionReleasesDeletedCreator(t *testing.T) {
	fixture := newCohortFixture(t)
	revision, _ := fixture.ensureExact(t)
	ctx, pool := fixture.ctx, fixture.pool
	creatorID := insertEntitlementActor(t, ctx, pool)
	fixture.actorID = creatorID
	maxStreams := revision.Policy.MaxStreams + 1
	derived, _ := fixture.derive(t, revision.ID, "Creator release "+uuid.NewString(), entitlements.PolicyPatch{MaxStreams: &maxStreams})

	_, err := pool.Exec(ctx, `UPDATE entitlement_policy_cohort_revisions SET name='renamed' WHERE id=$1`, derived.ID)
	requireImmutableCohortRevision(t, err)
	_, err = pool.Exec(ctx, `UPDATE entitlement_policy_cohort_revisions SET created_by_account_id=NULL, name='renamed' WHERE id=$1`, derived.ID)
	requireImmutableCohortRevision(t, err)

	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, creatorID)
	require.NoError(t, err)
	var creator *int
	var name string
	require.NoError(t, pool.QueryRow(ctx, `SELECT created_by_account_id,name FROM entitlement_policy_cohort_revisions WHERE id=$1`, derived.ID).Scan(&creator, &name))
	require.Nil(t, creator)
	require.Equal(t, derived.Name, name)

	_, err = pool.Exec(ctx, `DELETE FROM entitlement_policy_cohort_revisions WHERE id=$1`, derived.ID)
	requireImmutableCohortRevision(t, err)
}
