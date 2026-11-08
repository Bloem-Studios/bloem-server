package entitlements_test

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/entitlements"
)

// Cohort access groups show up wherever admins pick a group, so their names
// read like the cohort and stay unique without an ID fragment.
func TestCohortAccessGroupsUseReadableUniqueNames(t *testing.T) {
	fixture := newCohortFixture(t)
	parent, _ := fixture.ensureExact(t)
	disabled := false
	two := 2

	derive := func(patch entitlements.PolicyPatch) string {
		tx, err := fixture.pool.Begin(fixture.ctx)
		require.NoError(t, err)
		derived, created, err := fixture.store.DeriveCohortInTx(
			fixture.ctx, tx, fixture.organizationID, parent.ID, "Family", patch, fixture.actorID,
		)
		require.NoError(t, err)
		require.True(t, created)
		require.NoError(t, tx.Commit(fixture.ctx))
		var name string
		require.NoError(t, fixture.pool.QueryRow(fixture.ctx,
			`SELECT name FROM access_groups WHERE id=$1`, derived.AccessGroupID).Scan(&name))
		return name
	}

	require.Equal(t, "Family (cohort)", derive(entitlements.PolicyPatch{DownloadAllowed: &disabled, DownloadTranscodeAllowed: &disabled}))
	require.Equal(t, "Family (cohort 2)", derive(entitlements.PolicyPatch{MaxStreams: &two}))
}
