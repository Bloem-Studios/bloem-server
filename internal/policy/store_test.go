package policy

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newPolicyStoreTest(t *testing.T, ctx context.Context) (*pgxpool.Pool, *PolicyStore) {
	t.Helper()
	if policyStoreTestDatabaseConfig == nil {
		t.Skip("SILO_TEST_DATABASE_URL is not set; PostgreSQL store tests skip only outside SILO_REQUIRE_TEST_DATABASE=1")
	}
	pool, err := pgxpool.NewWithConfig(ctx, policyStoreTestDatabaseConfig.Copy())
	if err != nil {
		t.Fatalf("connect disposable policy-store database: %v", err)
	}
	t.Cleanup(pool.Close)

	resetPolicyTables(t, ctx, pool)
	return pool, NewPolicyStore(pool)
}

func resetPolicyTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `TRUNCATE TABLE public.policy_decisions RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate policy_decisions: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE TABLE public.policy_documents, public.policy_document_versions RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate policy documents: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.policy_generation (id, generation)
		VALUES (true, 1)
		ON CONFLICT (id)
		DO UPDATE SET generation = 1, updated_at = now()`); err != nil {
		t.Fatalf("reset policy generation: %v", err)
	}
}

func validStorePolicySource() string {
	return `package silo_custom.scope

import rego.v1

override(base, _) := base`
}
