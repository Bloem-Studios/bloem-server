package database

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestBloemRequestGroupMigrationAfterPolicyFinalization(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	pool := fixture.pool
	ctx := t.Context()
	provider, err := newMigrationProvider(pool, migrations.BloemFS, "sql")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if _, err := provider.UpTo(ctx, 20260926163857); err != nil {
		t.Fatal(err)
	}
	if changed, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil || !changed {
		t.Fatalf("finalize: changed=%t error=%v", changed, err)
	}
	pool.Reset()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := tenancy.MarkMembershipPolicyWriter(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE organization_memberships SET requests_allowed=true WHERE account_id=$1`, fixture.accountID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_user_limits(user_id,limit_mode) VALUES($1,'blocked')`, fixture.accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 20260926181741); err != nil {
		t.Fatalf("upgrade finalized Bloem schema: %v", err)
	}
	var total, blocked int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE requests_allowed=false) FROM organization_memberships WHERE account_id=$1`, fixture.accountID).Scan(&total, &blocked); err != nil {
		t.Fatal(err)
	}
	if total != 2 || blocked != total {
		t.Fatalf("memberships=%d blocked=%d; want both memberships blocked", total, blocked)
	}
	var mode string
	if err := pool.QueryRow(ctx, `SELECT limit_mode FROM request_user_limits WHERE user_id=$1`, fixture.accountID).Scan(&mode); err != nil || mode != "inherit" {
		t.Fatalf("limit mode=%q error=%v", mode, err)
	}
	if err := RunMigrations(ctx, pool, migrations.BloemFS, "sql"); err != nil {
		t.Fatalf("remaining migrations: %v", err)
	}
}
