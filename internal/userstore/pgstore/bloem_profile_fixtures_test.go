package pgstore

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// provisionTestMembership supplies the account prerequisite for profile
// creation through the same transaction-local policy writer as production.
// Provisioning rejects an existing membership with a different role or status.
func provisionTestMembership(t *testing.T, pool *pgxpool.Pool, userID int) {
	t.Helper()
	if _, err := tenancy.NewStore(pool).ProvisionDefaultMembership(t.Context(), userID, "user"); err != nil {
		t.Fatalf("provision test membership: %v", err)
	}
}
