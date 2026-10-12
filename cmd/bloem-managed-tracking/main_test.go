package main

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInvalidGrantArgumentsCannotExposeConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "private-database-value")
	t.Setenv("SECRET_KEY", "private-key")
	for _, args := range [][]string{{"-installation", "-1"}, {"-installation", "1", "-action", "activate"}, {"-installation", "1", "unexpected"}} {
		var out bytes.Buffer
		err := run(context.Background(), args, &out)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid command leaked config or succeeded")
		}
	}
}

// The operator's status command must not renew the credential generation;
// renewal belongs to the reconciler and never runs from a status check.
func TestInfoActionHasNoSideEffects(t *testing.T) {
	dsn := os.Getenv("BLOEM_MANAGED_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("explicit task-owned database required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || !strings.HasPrefix(cfg.ConnConfig.Database, "bloem_managed_test") {
		t.Fatal("refusing nonfixture database")
	}
	ctx := t.Context()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	name := "cli-" + uuid.NewString()
	var owner, install int
	if err = pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role) VALUES($1,$2,'x','admin') RETURNING id`, name, name+"@example.test").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO organizations(id,slug,name,status,owner_account_id) VALUES($1,$2,'CLI tenant','active',$3)`, tenant, tenant, owner); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path) VALUES('bloem.pastime','test','fixture') RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("SECRET_KEY", strings.Repeat("fixture-only-key-", 3))
	var out bytes.Buffer
	if err = run(ctx, []string{"-action", "grant", "-installation", strconv.Itoa(install), "-tenant", tenant}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE bloem_managed_grants SET renewed_at=clock_timestamp()-interval '31 days' WHERE installation_id=$1`, install); err != nil {
		t.Fatal(err)
	}
	var before, after uint64
	if err = pool.QueryRow(ctx, `SELECT generation FROM bloem_managed_grants WHERE installation_id=$1`, install).Scan(&before); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err = run(ctx, []string{"-action", "info", "-installation", strconv.Itoa(install)}, &out); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT generation FROM bloem_managed_grants WHERE installation_id=$1`, install).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before || !strings.Contains(out.String(), `"credential_generation":`+strconv.FormatUint(before, 10)) {
		t.Fatalf("info renewed the grant: %d -> %d (%s)", before, after, out.String())
	}
}
