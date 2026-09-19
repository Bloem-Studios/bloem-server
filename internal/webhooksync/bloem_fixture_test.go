package webhooksync

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// Seed the account's real membership policy before creating its profile. The
// canonical writers select the tenant/group and retain the policy-writer guard.
func bloemSyncProfileFixture(t *testing.T, pool *pgxpool.Pool, profileID, name string) (int, func()) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	identity := "webhook-fixture-" + uuid.NewString()
	account, err := auth.NewUserRepository(pool).CreateInTransaction(ctx, tx, models.CreateUserInput{
		Username: identity, Email: identity + "@example.test", Password: uuid.NewString(), Role: "user",
	})
	if err != nil {
		t.Fatalf("create sync account and membership: %v", err)
	}
	if err := pgstore.NewPostgresProvider(pool).CreateProfileInTransaction(ctx, tx, account.ID, userstore.Profile{ID: profileID, Name: name}); err != nil {
		t.Fatalf("create sync profile: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit sync fixture: %v", err)
	}
	return account.ID, func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id=$1", account.ID); err != nil {
			t.Errorf("clean up sync fixture: %v", err)
		}
	}
}
