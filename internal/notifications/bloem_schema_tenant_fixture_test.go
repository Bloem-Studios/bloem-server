package notifications

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedSchemaTenantProfiles gives a schemaDB pool its own organizations and
// user_profiles tables, holding one active organization with the named
// profiles. Bloem rechecks the recipient's organization before sending a
// queued notification (deliveryAccessPredicate), so a Silo test that sends
// one on a private schema needs a live recipient there. The tables shadow
// public's through the pool's search_path, so the shared database is left
// alone and parallel tests cannot collide on a profile id.
func seedSchemaTenantProfiles(t *testing.T, pool *pgxpool.Pool, profiles ...string) {
	t.Helper()
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE organizations (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), status text NOT NULL);
		CREATE TABLE user_profiles (id text PRIMARY KEY, organization_id uuid NOT NULL REFERENCES organizations(id));
		INSERT INTO organizations (status) VALUES ('active')`); err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_profiles (id, organization_id) SELECT $1, id FROM organizations`, profile); err != nil {
			t.Fatal(err)
		}
	}
}
