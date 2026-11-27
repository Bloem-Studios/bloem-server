package backup

import (
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/backup/backuptest"
)

// A restore keeps users.enabled at its stricter value; a household login's
// household_disabled mark has to come along, or a login the live server had
// switched off with its suspended main account would stay off for good when
// the customer resumes.
func TestRestoreKeepsTheHouseholdMarkDB(t *testing.T) {
	live, liveDSN := backuptest.Database(t)
	restored, restoredDSN := backuptest.Database(t)
	ctx := t.Context()
	// One account on both sides: the carry pairs accounts by id and creation
	// time, so both rows get one creation time.
	const username = "household-restore-login"
	if _, err := live.Exec(ctx, `INSERT INTO users (username, email, password_hash, role, enabled, household_disabled, created_at)
		VALUES ($1::text, $1::text || '@example.test', 'x', 'user', false, true, '2026-01-02T03:04:05Z')`, username); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Exec(ctx, `INSERT INTO users (username, email, password_hash, role, enabled, household_disabled, created_at)
		VALUES ($1::text, $1::text || '@example.test', 'x', 'user', true, false, '2026-01-02T03:04:05Z')`, username); err != nil {
		t.Fatal(err)
	}
	carried, err := PGLoader{}.CarryCredentials(ctx, restoredDSN, liveDSN)
	if err != nil {
		t.Fatal(err)
	}
	var enabled, householdDisabled bool
	if err := restored.QueryRow(ctx, `SELECT enabled, household_disabled FROM users WHERE username = $1`, username).Scan(&enabled, &householdDisabled); err != nil {
		t.Fatal(err)
	}
	if enabled || !householdDisabled {
		t.Fatalf("restored login enabled %v, household_disabled %v; want it off and marked to come back with its main account", enabled, householdDisabled)
	}
	if want := "account " + username + " kept to come back with its main account"; !slices.Contains(carried.Stricter, want) {
		t.Fatalf("reported %v, want %q", carried.Stricter, want)
	}
}
