package catalog

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// seedBloemCatalogProfiles uses the same membership writer and profile identity
// resolution as account provisioning. The fixture accounts have the user role;
// profile ownership does not grant account or organization administration.
// Catalog tests supply explicit AccessFilters; this helper does not seed the
// account policy needed by login or authorization tests.
func seedBloemCatalogProfiles(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int, profiles ...userstore.Profile) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin catalog profile fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tenancy.NewStore(pool).ProvisionDefaultMembershipInTransaction(ctx, tx, userID, "user"); err != nil {
		t.Fatalf("provision catalog membership: %v", err)
	}
	provider := pgstore.NewPostgresProvider(pool)
	for _, profile := range profiles {
		if err := provider.CreateProfileInTransaction(ctx, tx, userID, profile); err != nil {
			t.Fatalf("create catalog profile fixture: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit catalog profile fixture: %v", err)
	}
}
