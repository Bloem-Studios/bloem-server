package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestRevokeByUserAndSessionScopesTheMutation(t *testing.T) {
	pool := newSessionTestDatabase(t)
	userA := newSessionTestAccount(t, pool, "session-scope-a")
	userB := newSessionTestAccount(t, pool, "session-scope-b")
	repository := NewSessionRepository(pool)
	ctx := context.Background()

	for _, session := range []models.AuthSession{
		{ID: "scope-session-a", UserID: userA, ExpiresAt: time.Now().Add(time.Hour)},
		{ID: "scope-session-b", UserID: userB, ExpiresAt: time.Now().Add(time.Hour)},
	} {
		if err := repository.Create(ctx, session); err != nil {
			t.Fatalf("Create(%q): %v", session.ID, err)
		}
	}

	if err := repository.RevokeByUserAndSession(ctx, userA, "scope-session-b"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-user revoke error = %v, want ErrSessionNotFound", err)
	}
	if valid, err := repository.IsValid(ctx, "scope-session-b"); err != nil || !valid {
		t.Fatalf("foreign session validity = %v, %v; want active", valid, err)
	}

	if err := repository.RevokeByUserAndSession(ctx, userA, "scope-session-a"); err != nil {
		t.Fatalf("owned revoke: %v", err)
	}
	if valid, err := repository.IsValid(ctx, "scope-session-a"); err != nil || valid {
		t.Fatalf("owned session validity = %v, %v; want revoked", valid, err)
	}
}

func TestSessionRepositoryCallerTransactionControlsRevoke(t *testing.T) {
	pool := newSessionTestDatabase(t)
	userID := newSessionTestAccount(t, pool, "session-caller-tx")
	repository := NewSessionRepository(pool)
	ctx := context.Background()
	if err := repository.Create(ctx, models.AuthSession{ID: "caller-tx-one", UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin revoke: %v", err)
	}
	if err := repository.RevokeByUserAndSessionInTransaction(ctx, tx, userID, "caller-tx-one"); err != nil {
		t.Fatalf("revoke in transaction: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback revoke: %v", err)
	}
	if valid, err := repository.IsValid(ctx, "caller-tx-one"); err != nil || !valid {
		t.Fatalf("session after rollback valid=%v, error=%v", valid, err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin revoke: %v", err)
	}
	if err := repository.RevokeByUserAndSessionInTransaction(ctx, tx, userID, "caller-tx-one"); err != nil {
		t.Fatalf("revoke in transaction: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit revoke: %v", err)
	}
	if valid, err := repository.IsValid(ctx, "caller-tx-one"); err != nil || valid {
		t.Fatalf("session after commit valid=%v, error=%v", valid, err)
	}
}

// newSessionTestDatabase is a migrated disposable database with an activated
// server owner, so accounts can join the default organization.
func newSessionTestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newAuthTenancyDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	owner, err := NewUserRepository(pool).Create(ctx, models.CreateUserInput{
		Username: "session-owner", Email: "session-owner@example.test", Password: "owner-password", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create fixture owner: %v", err)
	}
	tenants := tenancy.NewStore(pool)
	if _, err := tenants.ProvisionDefaultMembership(ctx, owner.ID, "admin"); err != nil {
		t.Fatalf("provision fixture owner membership: %v", err)
	}
	if _, err := tenants.ActivateInitialOwnership(ctx, owner.ID); err != nil {
		t.Fatalf("activate fixture owner: %v", err)
	}
	return pool
}

// newSessionTestAccount creates an account in the default organization.
func newSessionTestAccount(t *testing.T, pool *pgxpool.Pool, suffix string) int {
	t.Helper()
	ctx := context.Background()
	user, err := NewUserRepository(pool).Create(ctx, models.CreateUserInput{
		Username: suffix + "-user", Email: "fixture-" + suffix + "@example.test", Password: "account-password", Role: "user",
	})
	if err != nil {
		t.Fatalf("create fixture account: %v", err)
	}
	if _, err := tenancy.NewStore(pool).ProvisionDefaultMembership(ctx, user.ID, "user"); err != nil {
		t.Fatalf("provision fixture membership: %v", err)
	}
	return user.ID
}
