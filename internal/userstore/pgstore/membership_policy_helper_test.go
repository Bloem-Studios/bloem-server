package pgstore

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

// TestMain finalizes the membership policy authority before any test runs.
//
// A freshly migrated database sits in the compatibility phase, which is a policy
// FREEZE: writes are fenced on users and frozen on organization_memberships, so
// nothing in this package can mutate policy. Finalization is the handover that
// lifts it, and it also renames the legacy policy columns off users -- which is
// why the fixtures here read and write organization_memberships instead.
//
// The prepared database belongs to this process so EXPLAIN fixtures are not
// affected by other packages' schema changes or statistics.
//
// With SILO_REQUIRE_TEST_DATABASE=1 a missing SILO_TEST_DATABASE_URL fails the
// package instead of letting its PostgreSQL tests skip (Bloem CI contract; the
// Silo test files themselves only skip).
func TestMain(m *testing.M) {
	os.Exit(bloemtestdb.RunIsolatedSuite(m.Run))
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
