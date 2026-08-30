package adminpeople

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// TestMain finalizes the membership policy authority before any test runs.
//
// A freshly migrated database sits in the compatibility phase, which is a policy
// FREEZE: writes are fenced on users and frozen on organization_memberships, so
// nothing in this package can mutate policy. Finalization is the handover that
// lifts it, and it also renames the legacy policy columns off users -- which is
// why the fixtures here read and write organization_memberships instead.
//
// It is idempotent, so packages sharing one database can each call it.
func TestMain(m *testing.M) {
	if dsn := os.Getenv("SILO_TEST_DATABASE_URL"); dsn != "" {
		ctx := context.Background()
		if pool, err := pgxpool.New(ctx, dsn); err == nil {
			if err := markTestDatabaseAsPolicyWriter(ctx, pool, dsn); err != nil {
				fmt.Fprintf(os.Stderr, "mark test database as policy writer: %v\n", err)
				pool.Close()
				os.Exit(1)
			}
			if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
				fmt.Fprintf(os.Stderr, "finalize membership policy authority: %v\n", err)
				pool.Close()
				os.Exit(1)
			}
			pool.Close()
		}
	}
	os.Exit(m.Run())
}

// execMembershipPolicy runs a write against organization_memberships policy
// columns. guard_membership_policy_write requires the v1 writer marker, and that
// marker is transaction-local (SET LOCAL), so the statement has to travel with it
// inside one transaction rather than going out on a bare pool Exec.
func execMembershipPolicy(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin membership policy write: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer','v1',true)`); err != nil {
		t.Fatalf("mark membership policy writer: %v", err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("membership policy write: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit membership policy write: %v", err)
	}
}

// markTestDatabaseAsPolicyWriter makes every new session on the test database
// the v1 membership policy writer and a capable node.
//
// In production those markers are set per transaction by the code that owns the
// write. Fixtures are not that code -- they seed rows directly from dozens of
// call sites -- so the test database declares it once at the database level,
// which is the same statement of fact: this deployment speaks the v1 protocol.
// ALTER DATABASE .. SET only affects sessions opened afterwards, which is why it
// runs in TestMain before any pool is built.
func markTestDatabaseAsPolicyWriter(ctx context.Context, pool *pgxpool.Pool, dsn string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return err
	}
	name := cfg.ConnConfig.Database
	for _, setting := range []string{"bloem.membership_policy_writer", "bloem.schema_capability_writer"} {
		value := "v1"
		if _, err := pool.Exec(ctx, fmt.Sprintf(
			"ALTER DATABASE %s SET %s = %s",
			pgx.Identifier{name}.Sanitize(), setting, pgx.Identifier{value}.Sanitize(),
		)); err != nil {
			return err
		}
	}
	return nil
}
