package progresssync

import (
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// TestBloemTenancySnapshotReadsRequireTenant proves WithBloemTenancy makes
// the in-transaction snapshot authority fail closed without a validated
// request tenant and resolve with one.
func TestBloemTenancySnapshotReadsRequireTenant(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated isolated PostgreSQL")
	}
	pool := bloemProgressDatabase(t, dsn)
	var ownerID int
	if err := pool.QueryRow(t.Context(), `SELECT id FROM users WHERE username='bootstrap-owner'`).Scan(&ownerID); err != nil {
		t.Fatalf("load fixture owner: %v", err)
	}
	provider := notifications.WrapUserStoreProvider(pgstore.NewPostgresProvider(pool), &notifications.System{})
	store, err := provider.ForUser(t.Context(), ownerID)
	if err != nil {
		t.Fatal(err)
	}
	const profileID = "bloem-tenancy-profile"
	if err := store.CreateProfile(t.Context(), userstore.Profile{ID: profileID, Name: "Synthetic profile"}); err != nil {
		t.Fatal(err)
	}
	engine, err := policy.NewEngine(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resolver := policy.NewViewerResolver(auth.NewUserRepository(pool), provider, nil, policy.NewPDP(engine), access.NewTenantGroupStore(pool)).
		WithBloemTenancy(resourcetenancy.NewStore(pool))
	service := NewService(pool, provider, catalog.NewServerSettingsRepo(pool), resolver).WithBloemTenancy()
	input := access.ResolveInput{UserID: ownerID, ProfileID: profileID}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()

	if _, err := service.resolveSnapshot(t.Context(), tx, input); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("snapshot authority without tenant error=%v, want fail closed", err)
	}
	ctx := bloemProgressTenantContext(t, pool, ownerID, profileID)
	scope, err := service.resolveSnapshot(ctx, tx, input)
	if err != nil {
		t.Fatalf("snapshot authority with tenant: %v", err)
	}
	if scope.UserID != ownerID || scope.ProfileID != profileID {
		t.Fatalf("scope=%+v, want account %d profile %s", scope, ownerID, profileID)
	}
}
