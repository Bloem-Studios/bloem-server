package database

import (
	"context"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
)

func TestBloemFunctionRebrandPreservesCanonicalNamesOnRollback(t *testing.T) {
	pool := newDisposableMigrationDatabase(t)
	ctx := context.Background()
	runAllMigrations(t, pool)

	// Model the pre-rebrand naming alongside a fresh database's canonical
	// functions. The parent body and column default exercise both text-based
	// references and PostgreSQL's OID-based dependencies.
	_, err := pool.Exec(ctx, `
CREATE FUNCTION public.vondel_rebrand_ci_child(value text) RETURNS text
LANGUAGE SQL IMMUTABLE AS $$ SELECT value || '-child' $$;
CREATE FUNCTION public.vondel_rebrand_ci_parent(value text) RETURNS text
LANGUAGE SQL IMMUTABLE AS $$ SELECT public.vondel_rebrand_ci_child(value) $$;
CREATE TABLE public.rebrand_ci_probe (
    value text DEFAULT public.vondel_rebrand_ci_parent('legacy')
);`)
	if err != nil {
		t.Fatalf("seed legacy function references: %v", err)
	}
	var originalOID uint32
	if err := pool.QueryRow(ctx, `SELECT 'public.vondel_rebrand_ci_parent(text)'::regprocedure::oid`).Scan(&originalOID); err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.FS.ReadFile("sql/20260903092016_rename_vondel_functions_to_bloem.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(migration), "-- +goose Down")
	if !ok {
		t.Fatal("rebrand migration has no down section")
	}
	for _, step := range []struct{ name, sql string }{{"up", up}, {"down", down}, {"up again", up}} {
		t.Run(step.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, step.sql); err != nil {
				t.Fatalf("apply rebrand %s: %v", step.name, err)
			}
			var canonical, legacy bool
			if err := pool.QueryRow(ctx, `SELECT
    to_regprocedure('public.bloem_default_organization_id()') IS NOT NULL,
    to_regprocedure('public.vondel_default_organization_id()') IS NOT NULL`).Scan(&canonical, &legacy); err != nil {
				t.Fatal(err)
			}
			if !canonical || legacy {
				t.Fatalf("canonical/legacy organization function = %t/%t, want true/false", canonical, legacy)
			}
			var renamedOID uint32
			if err := pool.QueryRow(ctx, `SELECT 'public.bloem_rebrand_ci_parent(text)'::regprocedure::oid`).Scan(&renamedOID); err != nil {
				t.Fatal(err)
			}
			if renamedOID != originalOID {
				t.Fatalf("function identity changed: %d != %d", renamedOID, originalOID)
			}
			var direct, defaultValue string
			if err := pool.QueryRow(ctx, `SELECT public.bloem_rebrand_ci_parent('direct')`).Scan(&direct); err != nil {
				t.Fatalf("renamed function body: %v", err)
			}
			if err := pool.QueryRow(ctx, `INSERT INTO public.rebrand_ci_probe DEFAULT VALUES RETURNING value`).Scan(&defaultValue); err != nil {
				t.Fatalf("renamed function default: %v", err)
			}
			if direct != "direct-child" || defaultValue != "legacy-child" {
				t.Fatalf("function/default values = %q/%q", direct, defaultValue)
			}
		})
	}
}
