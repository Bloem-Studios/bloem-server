//go:build bloem

package bridgeimport

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

func init() {
	completeExpectedImportedProfile = completeBloemImportedProfile
}

// Schema22 contains no tenant or direct-login identity. The fixture shim
// assigns the legacy account's organization and default group on import;
// direct credentials remain absent and start at the initial revision.
func completeBloemImportedProfile(t *testing.T, pool *pgxpool.Pool, fixture importFixture, profile *userstore.Profile) {
	t.Helper()
	organizationID, groupID, err := tenancy.NewProfileIdentityResolver(pool).ResolveLegacyProfileIdentity(t.Context(), fixture.identity.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if groupID == nil {
		var defaultGroupID int64
		if err := pool.QueryRow(t.Context(), `SELECT id FROM access_groups WHERE organization_id = $1 AND is_default`, organizationID).Scan(&defaultGroupID); err != nil {
			t.Fatal(err)
		}
		groupID = &defaultGroupID
	}
	profile.OrganizationID = organizationID.String()
	profile.AccessGroupID = groupID
	profile.CredentialRevision = 1
}
