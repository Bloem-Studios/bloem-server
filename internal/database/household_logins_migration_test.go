package database

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/migrations"
)

// A household login is a users row linked to its main account. The link may
// not point at the row itself, a main account with logins cannot be deleted
// by a plain DELETE (the account service deletes its logins first), and an
// account written without the new columns, as upstream Silo writes one, is
// not household-disabled.
func TestHouseholdLoginsMigration(t *testing.T) {
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
	var householdDisabled bool
	if err := pool.QueryRow(ctx, `SELECT household_disabled FROM users WHERE id=$1`, mainID).Scan(&householdDisabled); err != nil {
		t.Fatal(err)
	}
	if householdDisabled {
		t.Fatal("an account written without the column is household-disabled")
	}

	_, err = pool.Exec(ctx, `UPDATE users SET household_main_user_id=id WHERE id=$1`, mainID)
	if pgCode(err) != "23514" {
		t.Fatalf("self link: error %v; want check violation", err)
	}

	var loginID int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role,household_main_user_id) VALUES('login','login@example.test','x','user',$1) RETURNING id`, mainID).Scan(&loginID); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, mainID)
	if pgCode(err) != "23001" { // restrict_violation
		t.Fatalf("deleting a main account with a login: error %v; want foreign-key violation", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, loginID); err != nil {
		t.Fatalf("deleting the login: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, mainID); err != nil {
		t.Fatalf("deleting the main account after its login: %v", err)
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("household logins rollback: %v", err)
	}
	var column bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='users' AND column_name IN ('household_main_user_id','household_disabled'))`).Scan(&column); err != nil || column {
		t.Fatalf("columns left after rollback=%t error=%v", column, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("repeat upgrade: %v", err)
	}
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
