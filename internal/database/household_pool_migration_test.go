package database

import (
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
)

// A household's shared pool is a row on its main account, deleted with the
// account, never negative; a playback reservation written without a household,
// as every reservation before this one was, belongs to none.
func TestHouseholdPoolsMigration(t *testing.T) {
	pool := newDisposableMigrationDatabase(t)
	ctx := t.Context()
	provider, err := newMigrationProvider(pool, migrations.BloemFS, "sql")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool.Reset()

	var mainID int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role) VALUES('main','main@example.test','x','user') RETURNING id`).Scan(&mainID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_household_pools(main_user_id,max_streams,max_transcodes) VALUES($1,-1,0)`, mainID); pgCode(err) != "23514" {
		t.Fatalf("negative streams: error %v; want check violation", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_household_pools(main_user_id,max_streams,max_transcodes) VALUES($1,3,2)`, mainID); err != nil {
		t.Fatalf("pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, mainID); err != nil {
		t.Fatalf("deleting the main account: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bloem_household_pools WHERE main_user_id=$1`, mainID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("pool rows left after the account=%d error=%v", left, err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO playback_capacity_reservations(session_id,account_id,profile_id,is_transcode,lease_until) VALUES('pool-migration-session',7,'p',false,now()+interval '1 minute')`); err != nil {
		t.Fatalf("reservation: %v", err)
	}
	var household int64
	if err := pool.QueryRow(ctx, `SELECT household_id FROM playback_capacity_reservations WHERE session_id='pool-migration-session'`).Scan(&household); err != nil || household != 0 {
		t.Fatalf("household of a reservation written without one=%d error=%v", household, err)
	}

	// The version before the household pools migration.
	if err := MigrateDownTo(ctx, pool, migrations.BloemFS, "sql", 20261121090000); err != nil {
		t.Fatalf("household pools rollback: %v", err)
	}
	var leftover bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('bloem_household_pools') IS NOT NULL
		OR EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='playback_capacity_reservations' AND column_name='household_id')`).Scan(&leftover); err != nil || leftover {
		t.Fatalf("pool schema left after rollback=%t error=%v", leftover, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("repeat upgrade: %v", err)
	}
}
