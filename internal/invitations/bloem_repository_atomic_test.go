package invitations

// Bloem fixture plumbing and coverage for Silo's repository_atomic_test.go.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Match the production membership seam so generated profiles receive the
// organization ID returned by the real tenancy store in the same transaction.
type invitationMembershipProvisioner struct{ store *tenancy.Store }

func (p invitationMembershipProvisioner) ProvisionDefaultMembership(ctx context.Context, accountID int, role string) error {
	_, err := p.store.ProvisionDefaultMembership(ctx, accountID, role)
	return err
}

func (p invitationMembershipProvisioner) ProvisionDefaultMembershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	membership, err := p.store.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, role)
	return membership.OrganizationID, membership.ID, err
}

func (f atomicInvitationFixture) accounts(provider userstore.UserStoreProvider) *auth.AccountProvisioner {
	accounts := auth.NewAccountProvisioner(auth.NewUserRepository(f.pool), provider)
	accounts.SetMembershipProvisioner(invitationMembershipProvisioner{tenancy.NewStore(f.pool)})
	return accounts
}

func TestInvitationAtomicFixtureIsolationAndCleanup(t *testing.T) {
	var databaseName string
	t.Run("isolated writes", func(t *testing.T) {
		f := atomicInvitationDB(t)
		var schema string
		if err := f.pool.QueryRow(t.Context(), `SELECT current_database(), current_schema()`).Scan(&databaseName, &schema); err != nil {
			t.Fatal(err)
		}
		sourceConfig, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		if databaseName == sourceConfig.ConnConfig.Database || schema != "public" {
			t.Fatalf("fixture must use public in its own database, got %s/%s", databaseName, schema)
		}
		inv := f.invite(t, "isolated")
		if _, err := f.repo.Accept(t.Context(), inv.TokenHash, f.provision(pgstore.NewPostgresProvider(f.pool))); err != nil {
			t.Fatal(err)
		}
		f.counts(t, inv, 1, 1, 1)
	})
	if databaseName == "" {
		return // The existing optional-database skip happened in the subtest.
	}
	admin, err := pgxpool.New(t.Context(), os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var exists bool
	if err := admin.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, databaseName).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("invitation fixture database survived cleanup")
	}
}

func assertInvitationTriggerFailure(t *testing.T, err error, message string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "P0001" || pgErr.Message != message {
		t.Fatalf("expected trigger failure %q, got %v", message, err)
	}
}
