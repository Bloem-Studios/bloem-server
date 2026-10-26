package entitlements_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/entitlements"
)

// Bloem: deleting a library prunes it from every access group that lists it
// (prune_deleted_library_access_references), including a template-managed
// cohort group, whose revision is immutable and keeps listing it. The
// coherence checks compare only libraries that still exist, so the delete
// commits and the group stays coherent; a group that drops a library that
// still exists is refused as before.
func TestBloemDeletingACohortLibraryKeepsItsGroupCoherent(t *testing.T) {
	ctx, pool, store := entitlementTestStore(t)
	var organizationID uuid.UUID
	var defaultGroupID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT o.id,g.id FROM organizations o JOIN access_groups g ON g.organization_id=o.id AND g.is_default WHERE o.is_default`).Scan(&organizationID, &defaultGroupID))
	kept := insertEntitlementLibrary(t, ctx, pool, "cohort-kept-"+uuid.NewString(), true)
	deleted := insertEntitlementLibrary(t, ctx, pool, "cohort-deleted-"+uuid.NewString(), true)
	template, err := store.Create(ctx, entitlements.CreateTemplateInput{
		Key: entitlementTestKey(t, "cohort-library-delete"), Name: "Cohort library delete " + uuid.NewString(),
		Enabled: true, Policy: standardPolicy([]int{kept, deleted}),
	})
	require.NoError(t, err)
	actorID := insertEntitlementActor(t, ctx, pool)
	accountID := insertDirectEntitlementAccount(t, ctx, pool, organizationID, defaultGroupID, "cohort-library-delete")
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	applied, err := store.ApplyDefaultAccountTemplateInTransaction(ctx, tx, actorID, accountID, template.Key, template.Revision, false)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	var cohortID *uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT managed_cohort_id FROM access_groups WHERE id=$1`, applied.GroupID).Scan(&cohortID))
	if cohortID == nil {
		t.Fatal("the applied template's group is not cohort-managed")
	}

	_, err = pool.Exec(ctx, `DELETE FROM media_folders WHERE id=$1`, deleted)
	require.NoError(t, err, "deleting a library a cohort group lists")
	var libraries []int
	require.NoError(t, pool.QueryRow(ctx, `SELECT library_ids FROM access_groups WHERE id=$1`, applied.GroupID).Scan(&libraries))
	require.ElementsMatch(t, []int{kept}, libraries, "the cohort group keeps the libraries that still exist")

	_, err = pool.Exec(ctx, `UPDATE access_groups SET library_ids='{}' WHERE id=$1`, applied.GroupID)
	if err == nil || !strings.Contains(err.Error(), "managed cohort marker must match its immutable revision and policy") {
		t.Fatalf("dropping a library that still exists from a cohort group: err = %v, want the coherence refusal", err)
	}
}
